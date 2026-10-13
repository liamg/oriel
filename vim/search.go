package vim

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/liamg/oriel/vim/vimregex"
)

// Options are the engine's options that change how keys edit text (editor.md: options live at the layer they affect).
type Options struct {
	ShiftWidth int
	TabStop    int
	ExpandTab  bool
	IgnoreCase bool
	SmartCase  bool
	WrapScan   bool
	Magic      bool
	Report     int
	NrFormats  string // 'nrformats'
	Hidden     bool
	Clipboard  string // "system": the default register is the clipboard (unnamedplus); "registers": it isn't
}

// DefaultOptions are nvim's defaults.
func DefaultOptions() Options {
	return Options{ShiftWidth: 8, TabStop: 8, WrapScan: true, Magic: true, Report: 2, NrFormats: "bin,hex", Hidden: true, Clipboard: "system"}
}

// lastSearch is the last search pattern and offset, shared by /, ?, n, N, * and :s.
type lastSearch struct {
	pat    string
	fwd    bool
	off    searchOffset
	set    bool
	subPat string // the pattern of the last :s, which :~ and & reuse
}

// searchOffset is what follows a search pattern: a line offset, or an end, start or begin offset.
type searchOffset struct {
	kind byte // 0 (line offset), 'e', 's' or 'b'
	n    int
	set  bool
}

// input reads a snapshot line by line for vimregex.
type input struct{ v *view }

func (in input) Lines() int        { return in.v.lines() }
func (in input) Line(n int) []byte { return in.v.line(n) }

func (e *Engine) compile(pat string) (*vimregex.Regex, error) {
	return vimregex.Compile(pat, vimregex.Options{IgnoreCase: e.opts.IgnoreCase, SmartCase: e.opts.SmartCase, Magic: e.opts.Magic, LastSub: e.lastSub})
}

func (e *Engine) regexState(v *view) *vimregex.State {
	st := &vimregex.State{
		Cursor: vimregex.Pos{Line: e.cur.Line, Col: e.cur.Col},
		Mark: func(r rune) (vimregex.Pos, bool) {
			p, ok := e.markPos(r)
			return vimregex.Pos{Line: p.Line, Col: p.Col}, ok
		},
		VCol: func(line, col int) int {
			if line < 0 || line >= v.lines() || col >= maxCol {
				return col
			}
			return v.vcol(line, col)
		},
	}
	if a := e.lastVisual; a != nil {
		s, en := a.start, a.end
		if less(en, s) {
			s, en = en, s
		}
		if a.wantEnd && a.mode == ctrlV {
			en.Col = maxCol
		}
		s.Line, en.Line = min(s.Line, v.lines()-1), min(en.Line, v.lines()-1)
		st.Visual = &vimregex.Area{Start: vimregex.Pos{Line: s.Line, Col: s.Col}, End: vimregex.Pos{Line: en.Line, Col: en.Col}, Mode: byte(a.mode)}
	}
	return st
}

func toPos(p vimregex.Pos) Pos { return Pos{p.Line, p.Col} }

// findPattern is vim's searchit(): the count-th match after (fwd) or before p, wrapping around the buffer
// when 'wrapscan' is set. atCursor accepts a match at p itself.
func (e *Engine) findPattern(v *view, re *vimregex.Regex, p Pos, fwd bool, count int, atCursor bool) (vimregex.Match, bool, bool) {
	in := input{v}
	st := e.regexState(v)
	wrapped := false
	var m vimregex.Match
	for ; count > 0; count-- {
		found := false
		n := v.lines()
		for i := 0; i <= n; i++ {
			var line int
			if fwd {
				line = p.Line + i
			} else {
				line = p.Line - i
			}
			if line >= n || line < 0 {
				if !e.opts.WrapScan {
					return m, false, wrapped
				}
				wrapped = true
				line = (line%n + n) % n
			}
			first := i == 0
			last := i == n
			if fwd {
				c := 0
				for {
					mm, ok := re.Exec(in, line, c, st)
					if !ok {
						break
					}
					if first && !last {
						if mm.Start.Line == line && (mm.Start.Col < p.Col || mm.Start.Col == p.Col && !atCursor) {
							c = nextCol(v, line, max(c, mm.Start.Col))
							if c > v.lineLen(line) {
								break
							}
							continue
						}
					}
					if last && mm.Start.Col > p.Col {
						break
					}
					m, found = mm, true
					break
				}
			} else {
				c := 0
				var best vimregex.Match
				ok := false
				for c <= v.lineLen(line) {
					mm, got := re.Exec(in, line, c, st)
					if !got {
						break
					}
					if first && (mm.Start.Col > p.Col || mm.Start.Col == p.Col && !atCursor) {
						break
					}
					if last && mm.Start.Col <= p.Col {
						break
					}
					best, ok = mm, true
					c = nextCol(v, line, mm.Start.Col)
				}
				if ok {
					m, found = best, true
				}
			}
			if found {
				break
			}
		}
		if !found {
			return m, false, wrapped
		}
		p = toPos(m.Start)
		atCursor = false
	}
	return m, true, wrapped
}

// nextCol is the column after the character at col, or col+1 past the end of the line.
func nextCol(v *view, line, col int) int {
	if _, n := v.char(Pos{line, col}); n > 0 {
		return col + n
	}
	return col + 1
}

// parseSearch splits what was typed after / or ? into the pattern and the offset, at an unescaped delimiter.
func parseSearch(s string, delim byte) (pat, off string, hasOff bool) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '[':
			if j := skipCollection(s, i); j > i {
				i = j
			}
		case delim:
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// skipCollection returns the index of the ] closing a [] collection at i, or i when there is none.
func skipCollection(s string, i int) int {
	j := i + 1
	if j < len(s) && s[j] == '^' {
		j++
	}
	if j < len(s) && s[j] == ']' {
		j++
	}
	for ; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case ']':
			return j
		}
	}
	// An unclosed collection runs to the end, as vim's skip_regexp() has it.
	return len(s)
}

func parseOffset(s string) (searchOffset, bool) {
	var o searchOffset
	if s == "" {
		return o, true
	}
	o.set = true
	if c := s[0]; c == 'e' || c == 's' || c == 'b' {
		o.kind = c
		s = s[1:]
	}
	if s == "" {
		return o, true
	}
	if s == "+" || s == "-" {
		s += "1"
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return o, false
	}
	o.n = n
	return o, true
}

// searchMotion runs a / or ? search typed on the command line, with its offset and any ";" that chains another.
func (e *Engine) searchMotion(v *view, typed string, fwd bool, count int) (target, bool) {
	delim := byte('/')
	if !fwd {
		delim = '?'
	}
	p := e.cur
	var t target
	first := true
	for {
		pat, rest, _ := parseSearch(typed, delim)
		offText, next, chained := strings.Cut(rest, ";")
		if pat == "" {
			if !e.search.set {
				e.notify(errNoPrevPattern.with())
				return target{}, false
			}
			pat = e.search.pat
			if offText == "" && !strings.Contains(typed, string(delim)) {
				// "/<CR>" reuses the last offset too.
				offText = ""
			}
		}
		off, ok := parseOffset(offText)
		if !ok {
			return target{}, false
		}
		if pat == e.search.pat && !strings.Contains(typed, string(delim)) && first {
			off = e.search.off
		}
		e.search = lastSearch{pat: pat, fwd: fwd, off: off, set: true, subPat: e.search.subPat}
		var ok2 bool
		t, ok2 = e.runSearch(v, p, pat, fwd, off, max(count, 1))
		if !ok2 {
			return target{}, false
		}
		if !chained {
			break
		}
		p = t.pos
		count = 1
		first = false
		if next == "" {
			break
		}
		switch next[0] {
		case '/':
			fwd = true
			delim = '/'
		case '?':
			fwd = false
			delim = '?'
		default:
			return target{}, false
		}
		typed = next[1:]
	}
	return t, true
}

// runSearch finds pat from p and applies an offset, giving the motion's target.
func (e *Engine) runSearch(v *view, p Pos, pat string, fwd bool, off searchOffset, count int) (target, bool) {
	re, err := e.compile(pat)
	if err != nil {
		e.fail(err)
		return target{}, false
	}
	// A search from a closed fold starts after it, or backward from its first line.
	if top, end, ok := v.folded(p.Line); ok {
		if fwd {
			p = Pos{end, v.lineLen(end)}
		} else {
			p = Pos{top, 0}
		}
	}
	// With an e, s or b offset a match at the cursor counts, unless its offset position is the cursor.
	withOff := off.set && off.kind != 0
	m, ok, _ := e.findPattern(v, re, p, fwd, count, withOff)
	for i := 0; ok && withOff && offsetPos(v, m, off) == p && i < v.lines()+1; i++ {
		m, ok, _ = e.findPattern(v, re, toPos(m.Start), fwd, 1, false)
	}
	if !ok {
		e.notify(errPatternNotFound.with(pat))
		return target{}, false
	}
	t := target{pos: toPos(m.Start), jump: true}
	if off.set {
		switch off.kind {
		case 0:
			line := min(max(m.Start.Line+off.n, 0), v.lines()-1)
			t.pos = Pos{line, v.firstNonBlank(line)}
			t.kind = linewise
			t.keepCol = false
		case 'e':
			t.pos = offsetPos(v, m, off)
			t.kind = inclusive
		default:
			t.pos = offsetPos(v, m, off)
		}
	}
	return t, true
}

// offsetPos is where an e, s or b offset puts the cursor relative to a match.
func offsetPos(v *view, m vimregex.Match, off searchOffset) Pos {
	var p Pos
	if off.kind == 'e' {
		p = toPos(m.End)
		if p != toPos(m.Start) {
			v.dec(&p)
			if p.Col >= v.lineLen(p.Line) && p.Col > 0 {
				p.Col = v.lastCol(p.Line)
			}
		}
	} else {
		p = toPos(m.Start)
	}
	for n := off.n; n > 0; n-- {
		if v.inc(&p) == -1 {
			break
		}
		if p.Col >= v.lineLen(p.Line) && v.lineLen(p.Line) > 0 {
			v.inc(&p)
		}
	}
	for n := off.n; n < 0; n++ {
		if v.decl(&p) == -1 {
			break
		}
	}
	return p
}

func searchNext(e *Engine, v *view, m motionCtx) (target, bool) {
	if !e.search.set {
		e.notify(errNoPrevPattern.with())
		return target{}, false
	}
	fwd := e.search.fwd == (m.key == "n")
	return e.runSearch(v, e.cur, e.search.pat, fwd, e.search.off, m.count1())
}

func starSearch(e *Engine, v *view, m motionCtx) (target, bool) {
	l := v.line(e.cur.Line)
	in := func(c byte) bool { return isKeyword(rune(c)) }
	start := e.cur.Col
	for start < len(l) && !in(l[start]) {
		start++
	}
	keyword := start < len(l)
	if !keyword {
		// No keyword under or after the cursor: use the non-blank string instead.
		in = func(c byte) bool { return c != ' ' && c != '\t' }
		start = e.cur.Col
		for start < len(l) && !in(l[start]) {
			start++
		}
		if start >= len(l) {
			e.notify(errNoWord.with())
			return target{}, false
		}
	}
	for start > 0 && in(l[start-1]) {
		start--
	}
	end := start
	for end < len(l) && in(l[end]) {
		end++
	}
	word := escapePattern(string(l[start:end]))
	if keyword && (m.key == "*" || m.key == "#") {
		word = `\<` + word + `\>`
	}
	fwd := m.key == "*" || m.key == "g*"
	e.search = lastSearch{pat: word, fwd: fwd, set: true, subPat: e.search.subPat}
	return e.runSearch(v, Pos{e.cur.Line, start}, word, fwd, searchOffset{}, m.count1())
}

func escapePattern(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\/.*$^~[]`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// cmdline is the command line being typed after :, / or ?.
type cmdline struct {
	kind  byte // ':', '/' or '?'
	text  string
	cmd   cmd  // the operator or count it was started with
	vis   bool // started from Visual mode
	ctrlR bool
}

// startCmdline enters command-line mode.
func (e *Engine) startCmdline(kind byte, c cmd, prefill string) {
	e.cmdline = &cmdline{kind: kind, cmd: c, text: prefill, vis: e.visual != nil}
}

func (e *Engine) cmdlineKey(k string) {
	cl := e.cmdline
	if !cl.ctrlR && cl.kind == ':' {
		e.cmdlineAbbrev(cl, k)
	}
	if cl.ctrlR {
		cl.ctrlR = false
		if r, ok := argRune(k); ok {
			if t, ok := e.Register(r); ok {
				cl.text += strings.TrimSuffix(t, "\n")
			}
		}
		return
	}
	switch k {
	case "<Esc>", "<C-c>":
		e.cmdline = nil
		if cl.vis {
			e.endVisual()
		}
		e.rec = nil
		return
	case "<CR>", "<NL>", "<C-m>", "<C-j>":
		e.cmdline = nil
		e.runCmdline(cl)
		return
	case "<BS>", "<C-h>":
		if cl.text == "" {
			e.cmdline = nil
			return
		}
		_, n := utf8.DecodeLastRuneInString(cl.text)
		cl.text = cl.text[:len(cl.text)-n]
		return
	case "<C-u>":
		cl.text = ""
		return
	case "<C-r>":
		cl.ctrlR = true
		return
	case "<Tab>":
		cl.text += "\t"
		return
	}
	if r, ok := argRune(k); ok && r != '\r' {
		cl.text += string(r)
	}
}

func (e *Engine) runCmdline(cl *cmdline) {
	v := e.view()
	if cl.kind == ':' {
		if cl.vis && e.visual != nil {
			e.endVisual()
		}
		e.ex(v, cl.text)
		if strings.TrimSpace(cl.text) != "" {
			e.lastCmdline = cl.text
		}
		e.clamp(e.view())
		return
	}
	c := cl.cmd
	t, ok := e.searchMotion(v, cl.text, cl.kind == '/', c.count)
	if !ok {
		e.rec = nil
		if cl.vis {
			return
		}
		return
	}
	if e.visual != nil {
		e.setPcmark()
		e.cur = t.pos
		e.clampVisual(v)
		e.setWant(v)
		return
	}
	if c.op != "" {
		c.searchTarget = &t
		e.startChange(c)
		if e.rec != nil {
			e.rec.body = append(append([]string(nil), c.body...), keysOf(cl.text)...)
			e.rec.body = append(e.rec.body, "<CR>")
		}
		e.operator(v, c)
		if e.mode == Normal {
			e.endChange()
			e.clamp(e.view())
		}
		return
	}
	e.setPcmark()
	e.cur = t.pos
	e.clamp(v)
	e.setWant(v)
	if e.feeding == 0 {
		e.openCursorFold()
	}
}

// keysOf turns typed text back into keys, for "." to replay.
func keysOf(s string) []string {
	var out []string
	for _, r := range s {
		out = append(out, string(r))
	}
	return out
}
