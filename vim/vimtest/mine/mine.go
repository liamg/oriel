// Package mine converts vim's own tests (spec/vim/testdir) into vimtest cases.
//
// A test function is replayed statement by statement in nvim (a Sim). Keys typed with `:normal`,
// `:exe "normal …"` or feedkeys(…, 'x') since the last state change make a case's keys; the buffer
// and cursor before them are its given; the asserts that follow (vim's expectations) are its expect.
// Every case is then checked by replaying it alone in a fresh nvim (the Oracle).
package mine

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/vim/vimtest"
)

// Sim runs a test function's statements.
type Sim interface {
	Exec(src string) error
	Eval(expr string) (any, error)
	State() (vimtest.Buffer, string, error)
	Close() error
}

// Oracle replays a case alone, from its given, reading back the registers and marks it declares.
type Oracle func(c vimtest.Case) (vimtest.Result, error)

// Mined is a converted case with notes on where its values came from.
type Mined struct {
	Case  vimtest.Case
	Notes []string
}

// Drop is a case or assert that wasn't converted.
type Drop struct {
	Func, Category, Detail string
	Asserts                int
}

// Result is what mining one file found.
type Result struct {
	Funcs, Asserts, Converted int
	Cases                     []Mined
	Drops                     []Drop
}

// File mines one test file. name is its base name ("test_normal.vim").
func File(name, src string, newSim func() (Sim, error), oracle Oracle) Result {
	var res Result
	helpers := Helpers(src)
	for _, f := range Funcs(src) {
		r := mineFunc(name, f, helpers, newSim, oracle)
		res.Funcs++
		res.Asserts += r.Asserts
		res.Converted += r.Converted
		res.Cases = append(res.Cases, r.Cases...)
		res.Drops = append(res.Drops, r.Drops...)
	}
	return res
}

// MineFunc mines one function, for running functions in parallel. helpers is the file's other
// functions (see Helpers).
func MineFunc(name string, f Func, helpers string, newSim func() (Sim, error), oracle Oracle) Result {
	r := mineFunc(name, f, helpers, newSim, oracle)
	r.Funcs = 1
	return r
}

type assertion struct {
	lines  map[int]string // 0-based line → text
	all    bool           // lines is the whole buffer
	cline  *int           // cursor line, 0-based
	ccol   *int           // cursor byte column, 0-based
	cursor string         // the cursor expression asserted
	reg    rune
	regVal string
	mark   rune
	markAt *vimtest.Pos
	simOK  bool
}

// segment is the keys typed since the last state change. A case tries the keys since the previous
// asserts first, then, if those depend on what came before, all the segment's keys.
type segment struct {
	given  vimtest.Buffer
	keys   []vimtest.Key
	broken *Reject

	short       vimtest.Buffer // the state after the previous asserts
	from        int            // index in keys where short starts
	shortBroken *Reject

	asserts []assertion
}

func (s *segment) breakWith(r *Reject) {
	if s.broken == nil {
		s.broken = r
	}
	if s.shortBroken == nil {
		s.shortBroken = r
	}
}

type miner struct {
	file, fn string
	sim      Sim
	oracle   Oracle
	res      Result
	seg      *segment
	opts     map[string]bool // options touched
	vars     map[string]bool // variables holding literals
	dead     string          // why the rest of the function is skipped
	n        int             // assert blocks seen, for case names
}

func mineFunc(file string, f Func, helpers string, newSim func() (Sim, error), oracle Oracle) Result {
	m := &miner{file: file, fn: f.Name, oracle: oracle, opts: map[string]bool{}, vars: map[string]bool{}}
	sim, err := newSim()
	if err != nil {
		m.dead = "nvim: " + err.Error()
	} else {
		m.sim = sim
		defer sim.Close()
		if helpers != "" {
			sim.Exec(helpers)
		}
	}
	for _, st := range f.Stmts {
		m.stmt(st)
	}
	m.finish(true)
	return m.res
}

func countAsserts(s string) int { return len(assertRe.FindAllString(s, -1)) }

var assertRe = regexp.MustCompile(`\bassert_\w+\(`)

func (m *miner) drop(cat, detail string, asserts int) {
	if asserts == 0 && cat == "" {
		return
	}
	m.res.Drops = append(m.res.Drops, Drop{Func: m.fn, Category: cat, Detail: detail, Asserts: asserts})
}

func (m *miner) kill(reason string) {
	m.finish(true)
	m.dead = reason
}

var (
	exeRe      = regexp.MustCompile(`^(?::\s*)?exe(?:c|cu|cut|cute)?\s+(.+)$`)
	normPrefix = regexp.MustCompile(`^:*\s*norm(?:a|al)?!?\s*`)
	setRe      = regexp.MustCompile(`^(?::\s*)?(?:set|se|setl|setlocal|setg|setglobal)\s+(.*)$`)
	letOptRe   = regexp.MustCompile(`^let\s+&(?:l:|g:)?(\w+)\s*=\s*(.*)$`)
	letRe      = regexp.MustCompile(`^let\s+([A-Za-z_][\w:]*)\s*(=<<|=)\s*(.*)$`)
	mapRe      = regexp.MustCompile(`^(?::\s*)?(?:[nvxsoilct]?(?:nore)?map|no|nn|nnoremap|ino|vn|xn|ono|cno|[nvxsoilc]?(?:nore)?(?:ab|abbr|abbrev|abbreviate)|au|autocmd|aug|augroup|com|command|func|function|def|syn|syntax|filetype|lang|language|source|so|runtime)\b`)
	noopRe     = regexp.MustCompile(`^(?:redraw!?|redr!?|defer\b.*|unlet\b.*|delfunc\b.*|messages|mes|Check\w+\b.*)$`)
	localVarRe = regexp.MustCompile(`\bl:(\w)`)
	deferDelRe = regexp.MustCompile(`,\s*'D'\s*\)`)
)

func (m *miner) stmt(st Stmt) {
	asserts := countAsserts(st.Text)
	m.res.Asserts += asserts
	if m.dead != "" {
		m.drop(m.dead, "", asserts)
		return
	}
	t := st.Text
	if st.Block {
		for _, l := range strings.Split(st.Text, "\n") {
			if mm := letRe.FindStringSubmatch(strings.TrimSpace(l)); mm != nil && mm[2] == "=" {
				m.vars[mm[1]] = false
			}
		}
		if strings.HasPrefix(t, "let") {
			m.trackVars(t)
		}
		m.boundary(st, "control flow or embedded text", asserts)
		return
	}
	if noopRe.MatchString(t) {
		return
	}
	if keys, isKeys, err := keysOf(t); isKeys {
		m.keys(st, keys, err)
		return
	}
	if name, args, ok := parseCall(t); ok && strings.HasPrefix(name, "assert_") {
		m.assert(st, name, args)
		return
	}
	if mm := exeRe.FindStringSubmatch(t); mm != nil {
		k, err := concatKeys(mm[1])
		if err != nil {
			m.drop("non-literal :execute", t, asserts)
			m.kill("non-literal :execute")
			return
		}
		t = vimtest.Notation(k)
	}
	if mapRe.MatchString(t) {
		m.drop("mappings/autocmds/functions", strings.Fields(t)[0], asserts)
		m.kill("mappings/autocmds/functions")
		return
	}
	m.trackOptions(t)
	m.trackVars(t)
	if asserts > 0 {
		m.drop("unsupported assert", "unparsed: "+t, asserts)
	}
	m.boundary(st, "", 0)
}

// boundary runs a statement that changes state other than through keys: the next keys start a new case.
func (m *miner) boundary(st Stmt, why string, asserts int) {
	m.finish(true)
	if why != "" {
		m.drop(why, "", asserts)
	}
	if err := m.sim.Exec(topLevel(st.Text)); err != nil {
		m.kill("statement failed in nvim")
		m.drop("statement failed in nvim", firstLine(st.Text)+": "+err.Error(), 0)
	}
}

// topLevel adapts a function's statement to run outside a function: l: variables become global,
// and files written for deferred deletion are left for the replay's temporary directory.
func topLevel(s string) string {
	return deferDelRe.ReplaceAllString(localVarRe.ReplaceAllString(s, "g:$1"), ")")
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

func (m *miner) trackOptions(t string) {
	if mm := setRe.FindStringSubmatch(t); mm != nil {
		for _, a := range strings.Fields(mm[1]) {
			name := optArgRe.FindString(a)
			for _, p := range []string{"no", "inv"} {
				if strings.HasPrefix(name, p) && len(name) > len(p)+1 {
					name = strings.TrimPrefix(name, p)
				}
			}
			if name != "" {
				m.opts[name] = true
			}
		}
		return
	}
	if mm := letOptRe.FindStringSubmatch(t); mm != nil {
		m.opts[mm[1]] = true
	}
}

// nonDefault lists the options touched so far that differ from nvim's defaults, which a case runs with.
func (m *miner) nonDefault() []string {
	var out []string
	for _, n := range sortedSet(m.opts) {
		v, err := m.sim.Eval(fmt.Sprintf("&%s ==# nvim_get_option_info2('%s', {}).default", n, n))
		if b, ok := v.(int64); err == nil && ok && b == 1 {
			delete(m.opts, n)
			continue
		}
		out = append(out, n)
	}
	return out
}

var optArgRe = regexp.MustCompile(`^[a-z]+`)

func (m *miner) trackVars(t string) {
	mm := letRe.FindStringSubmatch(t)
	if mm == nil {
		return
	}
	name := mm[1]
	if mm[2] == "=<<" {
		m.vars[name] = !strings.Contains(strings.SplitN(mm[3], "\n", 2)[0], "eval")
		return
	}
	m.vars[name] = isLiteral(mm[3], m.vars)
}

// keysOf reads the keys a statement types. isKeys is false when it doesn't type keys; err is set when it
// does but they can't be read faithfully.
func keysOf(t string) (keys []vimtest.Key, isKeys bool, err *Reject) {
	if normalRe.MatchString(t) {
		loc := normalRe.FindStringIndex(t)
		rest := strings.TrimLeft(t[loc[1]:], " \t")
		if rest == "" {
			return nil, false, nil
		}
		return rawKeys(rest), true, nil
	}
	if mm := exeRe.FindStringSubmatch(t); mm != nil {
		k, perr := concatKeys(mm[1])
		if perr != nil {
			if strings.Contains(mm[1], "norm") {
				return nil, true, reject("keys not literal", "%s", t)
			}
			return nil, false, nil
		}
		var prefix strings.Builder
		for _, key := range k {
			if key.Name != "" || key.Mods != 0 {
				break
			}
			prefix.WriteRune(key.Rune)
		}
		loc := normPrefix.FindStringIndex(prefix.String())
		if loc == nil {
			return nil, false, nil
		}
		return k[len([]rune(prefix.String()[:loc[1]])):], true, nil
	}
	if name, args, ok := parseCall(t); ok && name == "feedkeys" {
		if len(args) < 2 {
			return nil, true, reject("feedkeys without 'x'", "%s", t)
		}
		flags, _, ferr := stringLit(args[1])
		f := vimtest.Notation(flags)
		if ferr != nil || !strings.Contains(f, "x") || strings.Contains(f, "!") {
			return nil, true, reject("feedkeys without 'x'", "%s", t)
		}
		k, perr := concatKeys(args[0])
		if perr != nil {
			return nil, true, reject("keys not literal", "%s", t)
		}
		return k, true, nil
	}
	return nil, false, nil
}

func (m *miner) keys(st Stmt, keys []vimtest.Key, kerr *Reject) {
	buf, mode, err := m.sim.State()
	switch {
	case m.seg == nil:
		m.seg = &segment{given: buf, short: buf}
	case len(m.seg.asserts) > 0:
		m.finish(false)
		m.seg.short, m.seg.from, m.seg.shortBroken = buf, len(m.seg.keys), nil
	}
	if err != nil || mode != vimtest.ModeNormal {
		m.seg.breakWith(reject("not in Normal mode before the keys", "%s", mode))
	}
	if opts := m.nonDefault(); len(opts) > 0 {
		m.seg.breakWith(reject("options set", "%s", strings.Join(opts, " ")))
	}
	if kerr != nil {
		m.seg.breakWith(kerr)
	}
	if m.seg.broken == nil || m.seg.shortBroken == nil {
		insert, err := Scan(keys)
		if err != nil {
			m.seg.breakWith(err)
		} else {
			m.seg.keys = append(m.seg.keys, keys...)
			if insert {
				m.seg.keys = append(m.seg.keys, vimtest.Key{Name: "Esc"})
			}
		}
	}
	if err := m.sim.Exec(topLevel(st.Text)); err != nil {
		m.kill("statement failed in nvim")
		m.drop("statement failed in nvim", st.Text+": "+err.Error(), 0)
	}
}

func sortedSet(s map[string]bool) []string {
	var out []string
	for k := range s {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// pureFuncs only read state, so an assert calling nothing else can be skipped without replaying it.
var pureFuncs = set("getline", "getpos", "getcurpos", "col", "line", "virtcol", "charcol", "getreg", "getregtype",
	"getreginfo", "len", "string", "getjumplist", "getchangelist", "getmarklist", "strlen", "strpart", "repeat",
	"range", "join", "split", "getbufline", "line2byte", "indent", "mode", "exists", "has", "type", "tr",
	"toupper", "tolower", "winsaveview", "winline", "wincol", "winnr", "winheight", "winwidth", "bufname",
	"fnamemodify", "foldlevel", "foldclosed", "foldclosedend", "visualmode", "getcharpos", "getcursorcharpos",
	"virtcol2col", "undotree", "changenr", "getbufvar", "getwinvar", "bufnr", "tabpagenr", "getqflist",
	"getloclist", "printf", "screenrow", "screencol", "screenchar", "trim", "matchstr", "substitute", "map",
	"filter", "get", "keys", "values", "count", "index", "reverse", "sort", "copy", "deepcopy", "char2nr",
	"nr2char", "strcharpart", "strchars", "strdisplaywidth", "getcmdline", "getcharsearch", "abs", "max",
	"min", "eval", "empty", "getmatches", "expand", "readfile", "bufexists", "buflisted", "getbufinfo",
	"getwininfo", "getcmdwintype", "state", "reg_recording", "reg_executing", "searchcount", "getregion",
	"getregionpos", "shiftwidth", "getcharstr", "execute", "uniq", "slice", "str2nr", "byteidx", "charidx")

var callNameRe = regexp.MustCompile(`([A-Za-z_][\w#.:]*)\(`)

func callsOnlyPure(s string) bool {
	for _, mm := range callNameRe.FindAllStringSubmatch(stripStrings(s), -1) {
		if !pureFuncs[mm[1]] {
			return false
		}
	}
	return true
}

func stripStrings(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '@' && i+1 < len(s) {
			b.WriteString(s[i : i+2])
			i++
			continue
		}
		if s[i] == '\'' || s[i] == '"' {
			i = skipString(s, i) - 1
			b.WriteString("''")
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (m *miner) assert(st Stmt, name string, args []string) {
	pure := callsOnlyPure(strings.Join(args, ","))
	if name != "assert_equal" || len(args) < 2 {
		if !pure || name == "assert_fails" || name == "assert_beeps" || name == "assert_nobeep" {
			m.boundary(st, "", 0)
		}
		m.drop("unsupported assert", name, 1)
		return
	}
	if !pure {
		m.boundary(st, "", 0)
		m.drop("unsupported assert", "calls a function with side effects", 1)
		return
	}
	want, got := args[0], args[1]
	a, ok := m.actual(got)
	if !ok {
		if a, ok = m.actual(want); ok {
			want, got = got, want
		}
	}
	if !ok {
		m.drop("unsupported assert", "assert_equal(_, "+got+")", 1)
		return
	}
	if !isLiteral(want, m.vars) {
		m.drop("expected value isn't a literal", want, 1)
		return
	}
	if m.seg == nil {
		m.drop("no keys before the assert", "", 1)
		return
	}
	if m.seg.broken != nil && m.seg.shortBroken != nil {
		m.drop(m.seg.shortBroken.Category, m.seg.shortBroken.Detail, 1)
		return
	}
	wv, err1 := m.sim.Eval(want)
	gv, err2 := m.sim.Eval(got)
	if err1 != nil || err2 != nil {
		m.drop("assert failed to evaluate in nvim", fmt.Sprint(err1, err2), 1)
		return
	}
	if !fill(&a, wv) {
		m.drop("unsupported assert", "expected value has an unexpected shape: "+want, 1)
		return
	}
	a.simOK = reflect.DeepEqual(wv, gv)
	m.seg.asserts = append(m.seg.asserts, a)
	if !a.simOK {
		// nvim's replay has diverged from vim, so later givens can't be trusted.
		m.kill("nvim diverged from vim earlier in the function")
	}
}

var (
	getlineRe = regexp.MustCompile(`^getline\(\s*('[.$]'|"[.$]"|\d+)\s*(?:,\s*('[.$]'|"[.$]"|\d+)\s*)?\)$`)
	cursorRe  = regexp.MustCompile(`^(?:getpos\(\s*(?:'\.'|"\.")\s*\)|getcurpos\(\))$`)
	colRe     = regexp.MustCompile(`^col\(\s*(?:'\.'|"\.")\s*\)$`)
	lineRe    = regexp.MustCompile(`^line\(\s*(?:'\.'|"\.")\s*\)$`)
	lineColRe = regexp.MustCompile(`^\[\s*line\(\s*(?:'\.'|"\.")\s*\)\s*,\s*col\(\s*(?:'\.'|"\.")\s*\)\s*\]$`)
	getregRe  = regexp.MustCompile(`^(?:getreg\(\s*(?:'(.)'|"(.)"|(\d))?\s*\)|@(.))$`)
	markPosRe = regexp.MustCompile(`^getpos\(\s*(?:"'(.)"|'''(.)')\s*\)$`)
)

// actual recognises the expressions an assert can check; the expected value is filled in later.
func (m *miner) actual(expr string) (assertion, bool) {
	expr = strings.TrimSpace(expr)
	var a assertion
	switch {
	case getlineRe.MatchString(expr):
		mm := getlineRe.FindStringSubmatch(expr)
		from, ok1 := m.lineNr(mm[1])
		to, ok2 := from, true
		if mm[2] != "" {
			to, ok2 = m.lineNr(mm[2])
			a.all = from == 0 && strings.Contains(mm[2], "$")
		}
		if !ok1 || !ok2 || to < from {
			return a, false
		}
		a.lines = map[int]string{}
		for l := from; l <= to; l++ {
			a.lines[l] = ""
		}
		if mm[2] == "" {
			a.lines[-1] = "" // a single line: getline() returns a string, not a list
		}
		return a, true
	case cursorRe.MatchString(expr), colRe.MatchString(expr), lineRe.MatchString(expr), lineColRe.MatchString(expr):
		a.cursor = expr
		return a, true
	case getregRe.MatchString(expr):
		mm := getregRe.FindStringSubmatch(expr)
		r := mm[1] + mm[2] + mm[3] + mm[4]
		if r == "" {
			r = `"`
		}
		c := []rune(strings.ToLower(r))[0]
		if !strings.ContainsRune(`abcdefghijklmnopqrstuvwxyz0123456789"-`, c) {
			return a, false
		}
		a.reg = c
		return a, true
	case markPosRe.MatchString(expr):
		mm := markPosRe.FindStringSubmatch(expr)
		c := []rune(mm[1] + mm[2])[0]
		if !strings.ContainsRune(`abcdefghijklmnopqrstuvwxyz[]'.^`, c) {
			return a, false
		}
		a.mark = c
		return a, true
	}
	return a, false
}

// lineNr resolves a getline() argument against the replay, 0-based.
func (m *miner) lineNr(arg string) (int, bool) {
	arg = strings.Trim(arg, `'"`)
	switch arg {
	case ".":
		v, err := m.sim.Eval("line('.')")
		n, ok := v.(int64)
		return int(n) - 1, err == nil && ok
	case "$":
		v, err := m.sim.Eval("line('$')")
		n, ok := v.(int64)
		return int(n) - 1, err == nil && ok
	}
	n, err := strconv.Atoi(arg)
	return n - 1, err == nil && n >= 1
}

// fill sets the assertion's expectation from vim's expected value.
func fill(a *assertion, v any) bool {
	switch {
	case a.lines != nil:
		_, single := a.lines[-1]
		delete(a.lines, -1)
		var vals []any
		if single {
			vals = []any{v}
		} else if l, ok := v.([]any); ok {
			vals = l
		} else {
			return false
		}
		keys := make([]int, 0, len(a.lines))
		for k := range a.lines {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if len(vals) != len(keys) && !a.all {
			return false
		}
		a.lines = map[int]string{}
		for i, x := range vals {
			s, ok := x.(string)
			if !ok {
				return false
			}
			a.lines[keys[0]+i] = s
		}
		if a.all {
			a.lines[-1] = fmt.Sprint(len(vals))
		}
		return true
	case a.cursor != "":
		expr := a.cursor
		num := func(x any) (int, bool) { n, ok := x.(int64); return int(n), ok }
		set := func(p *int, x any, off int) bool {
			n, ok := num(x)
			if !ok || n-off < 0 {
				return false
			}
			*p = n - off
			return true
		}
		a.cline, a.ccol = new(int), new(int)
		switch {
		case colRe.MatchString(expr):
			a.cline = nil
			return set(a.ccol, v, 1)
		case lineRe.MatchString(expr):
			a.ccol = nil
			return set(a.cline, v, 1)
		case lineColRe.MatchString(expr):
			l, ok := v.([]any)
			return ok && len(l) == 2 && set(a.cline, l[0], 1) && set(a.ccol, l[1], 1)
		default:
			l, ok := v.([]any)
			return ok && len(l) >= 4 && set(a.cline, l[1], 1) && set(a.ccol, l[2], 1)
		}
	case a.reg != 0:
		s, ok := v.(string)
		a.regVal = s
		return ok
	case a.mark != 0:
		l, ok := v.([]any)
		if !ok || len(l) < 4 {
			return false
		}
		line, ok1 := l[1].(int64)
		col, ok2 := l[2].(int64)
		if !ok1 || !ok2 {
			return false
		}
		if line == 0 {
			return true
		}
		if col < 1 {
			return false
		}
		a.markAt = &vimtest.Pos{Line: int(line) - 1, Col: int(col) - 1}
		return true
	}
	return false
}

// finish turns the open segment's asserts into a case, and closes the segment when end is set.
func (m *miner) finish(end bool) {
	seg := m.seg
	if end {
		m.seg = nil
	}
	if seg == nil || len(seg.asserts) == 0 {
		return
	}
	asserts := seg.asserts
	seg.asserts = nil
	n := len(asserts)
	m.n++
	name := fmt.Sprintf("%s #%d", m.fn, m.n)
	var cat, detail string
	if seg.shortBroken == nil {
		mined, c, d := m.build(name, seg.short, seg.keys[seg.from:], asserts)
		if mined != nil {
			m.keep(*mined, n)
			return
		}
		cat, detail = c, d
	}
	if seg.broken == nil && (seg.from > 0 || seg.shortBroken != nil) {
		mined, c, d := m.build(name, seg.given, seg.keys, asserts)
		if mined != nil {
			m.keep(*mined, n)
			return
		}
		cat, detail = c, d
	}
	if cat == "" {
		cat, detail = seg.shortBroken.Category, seg.shortBroken.Detail
	}
	m.drop(cat, detail, n)
}

func (m *miner) keep(c Mined, asserts int) {
	m.res.Converted += asserts
	m.res.Cases = append(m.res.Cases, c)
}

// build makes a case from keys played on given, checked against the oracle. It returns why not on failure.
func (m *miner) build(name string, given vimtest.Buffer, keys []vimtest.Key, asserts []assertion) (*Mined, string, string) {
	c := vimtest.Case{
		Name:    name,
		Ref:     "testdir/" + m.file + "#" + m.fn,
		Given:   given,
		Keys:    keys,
		RawKeys: notation(keys),
	}
	simOK := true
	for _, a := range asserts {
		simOK = simOK && a.simOK
		if a.reg != 0 {
			if c.Regs == nil {
				c.Regs = map[rune]*string{}
			}
			v := a.regVal
			c.Regs[a.reg] = &v
		}
		if a.mark != 0 {
			if c.Marks == nil {
				c.Marks = map[rune]*vimtest.Pos{}
			}
			c.Marks[a.mark] = a.markAt
		}
	}
	got, err := m.oracle(c)
	if err != nil {
		return nil, "oracle could not run the keys", err.Error()
	}
	lines := slices.Clone(got.Lines)
	all := false
	fromVim := map[int]bool{}
	cursor := got.Cursor
	var lineSet, colSet bool
	for _, a := range asserts {
		if a.lines != nil {
			if cnt, ok := a.lines[-1]; ok && a.all {
				want, _ := strconv.Atoi(cnt)
				all = true
				lines = make([]string, want)
				for i := range want {
					lines[i] = a.lines[i]
					fromVim[i] = true
				}
				continue
			}
			for l, s := range a.lines {
				if l < 0 {
					continue
				}
				for len(lines) <= l {
					lines = append(lines, "")
				}
				lines[l] = s
				fromVim[l] = true
			}
		}
		if a.cline != nil {
			cursor.Line, lineSet = *a.cline, true
		}
		if a.ccol != nil {
			cursor.Col, colSet = *a.ccol, true
		}
	}
	c.Expect = vimtest.Buffer{Lines: lines, Cursor: cursor}
	var notes []string
	switch {
	case !lineSet && !colSet:
		notes = append(notes, "cursor from oracle")
	case !lineSet:
		notes = append(notes, "cursor line from oracle")
	case !colSet:
		notes = append(notes, "cursor column from oracle")
	}
	if !all {
		var from []int
		for i := range lines {
			if !fromVim[i] {
				from = append(from, i+1)
			}
		}
		if len(from) > 0 {
			notes = append(notes, "lines "+ranges(from)+" from oracle")
		}
	}
	if cursor.Line >= len(lines) || cursor.Col > len(lines[cursor.Line]) {
		return nil, "nvim disagrees with vim", name + ": cursor outside the buffer"
	}
	if diffs := c.Diff(got); len(diffs) > 0 {
		cat := "nvim disagrees with vim"
		if simOK {
			cat = "depends on state before the keys"
		}
		return nil, cat, name + ": keys " + c.RawKeys + ": " + strings.ReplaceAll(strings.Join(diffs, "; "), "\n", " ")
	}
	if err := roundTrip(c); err != nil {
		return nil, "not expressible as a .vt case", err.Error()
	}
	return &Mined{Case: c, Notes: notes}, "", ""
}

func ranges(ns []int) string {
	var parts []string
	for i := 0; i < len(ns); {
		j := i
		for j+1 < len(ns) && ns[j+1] == ns[j]+1 {
			j++
		}
		if i == j {
			parts = append(parts, strconv.Itoa(ns[i]))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", ns[i], ns[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}
