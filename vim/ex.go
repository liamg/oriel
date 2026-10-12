package vim

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/vim/vimregex"
)

// exError is an ex command's error, shown as a message.
type exError string

func (e exError) Error() string { return string(e) }

// exRange is the lines an ex command works on, 0-based and inclusive.
type exRange struct {
	from, to int
	given    int // how many addresses were typed
}

// ex runs one ex command line.
func (e *Engine) ex(v *view, line string) {
	e.group = e.ed.NewGroup()
	if err := e.exec1(v, line); err != nil {
		e.echo(err.Error())
	}
	e.group = 0
}

func (e *Engine) exec1(v *view, line string) error {
	line = strings.TrimLeft(line, " \t:")
	if line == "" {
		return nil
	}
	for _, mod := range []string{"silent!", "silent", "sil!", "sil"} {
		if rest, ok := strings.CutPrefix(line, mod); ok && (rest == "" || rest[0] == ' ') {
			e.silent = true
			defer func() { e.silent = false }()
			line = strings.TrimLeft(rest, " ")
			break
		}
	}
	r, rest, err := e.parseRange(v, line)
	if err != nil {
		return err
	}
	rest = strings.TrimLeft(rest, " \t")
	name, args := splitCommand(rest)
	if name == "" {
		if r.given == 0 {
			return nil
		}
		r.to = min(max(r.to, 0), v.lines()-1)
		e.cur = Pos{r.to, 0}
		e.cur.Col = v.colAt(r.to, e.want, false)
		if e.cur.Col == 0 {
			e.cur.Col = 0
		}
		e.setWant(v)
		return nil
	}
	if r.from == -1 && name != "pu" && name != "put" {
		r.from = 0
		r.to = max(r.to, 0)
	}
	if r.from > r.to {
		if e.silent {
			r.from, r.to = r.to, r.from
		} else {
			r.from, r.to = r.to, r.from
			e.confirm = &confirmation{prompt: "Backwards range given, OK to swap (y/n)?", yes: func() {
				e.group = e.ed.NewGroup()
				if err := e.runCommand(e.view(), r, name, args); err != nil {
					e.echo(err.Error())
				}
				e.group = 0
			}}
			e.echo(e.confirm.prompt)
			return nil
		}
	}
	if r.to >= v.lines() || r.from < -1 {
		return exError("E16: Invalid range")
	}
	return e.runCommand(v, r, name, args)
}

// runCommand runs a parsed ex command over its range.
func (e *Engine) runCommand(v *view, r exRange, name, args string) error {
	bang := false
	if strings.HasPrefix(args, "!") && name != "s" && name != "&" {
		bang = true
		args = args[1:]
	}
	switch name {
	case "d", "delete":
		return e.exDelete(v, r, args)
	case "y", "yank":
		return e.exYank(v, r, args)
	case "m", "move":
		return e.exMove(v, r, args)
	case "t", "co", "copy":
		return e.exCopy(v, r, args)
	case "j", "join":
		return e.exJoin(v, r, args, bang)
	case ">", "<":
		return e.exShift(v, r, name, args)
	case "norm", "normal":
		return e.exNormal(v, r, args)
	case "pu", "put":
		return e.exPut(v, r, args, bang)
	case "s", "substitute":
		return e.exSubstitute(v, r, args, false)
	case "&":
		return e.exSubstitute(v, r, args, true)
	case "&&":
		return e.exSubstitute(v, r, "&"+args, true)
	case "~":
		return e.exTilde(v, r, args)
	case "g", "global", "v", "vglobal":
		return e.exGlobal(v, r, args, bang || name[0] == 'v', r.given == 0)
	case "set", "se":
		return e.exSet(args)
	case "p", "print", "#", "nu", "number", "l", "list":
		e.cur = Pos{r.to, v.firstNonBlank(r.to)}
		return nil
	}
	return exError("E492: Not an editor command: " + strings.TrimSpace(name+args))
}

// splitCommand splits a command line after its range into the command name and its arguments.
func splitCommand(s string) (string, string) {
	if s == "" {
		return "", ""
	}
	switch c := s[0]; {
	case c == '&' && strings.HasPrefix(s, "&&"):
		return "&&", s[2:]
	case c == '&' || c == '~' || c == '#':
		return string(c), s[1:]
	case c == '>' || c == '<':
		i := 0
		for i < len(s) && s[i] == c {
			i++
		}
		return string(c), strings.Repeat(string(c), i-1) + s[i:]
	}
	i := 0
	for i < len(s) && unicode.IsLetter(rune(s[i])) {
		i++
	}
	name := s[:i]
	// :s and :g take any delimiter right after the name.
	for _, short := range []string{"s", "g", "v"} {
		if strings.HasPrefix(name, short) && len(name) > 1 {
			full := map[string]string{"s": "substitute", "g": "global", "v": "vglobal"}[short]
			if !strings.HasPrefix(full, name) && !strings.HasPrefix(name, "se") && !strings.HasPrefix(name, "norm") {
				return short, s[1:]
			}
		}
	}
	for _, long := range []string{"normal", "delete", "yank", "move", "copy", "join", "put", "substitute", "global", "vglobal", "print", "number", "list", "set"} {
		if len(name) > 1 && strings.HasPrefix(long, name) {
			return long, s[i:]
		}
	}
	return name, s[i:]
}

// parseRange reads the addresses at the start of a command line.
func (e *Engine) parseRange(v *view, s string) (exRange, string, error) {
	cur := e.cur.Line
	r := exRange{from: cur, to: cur}
	if strings.HasPrefix(s, "%") {
		r = exRange{from: 0, to: v.lines() - 1, given: 2}
		return r, s[1:], nil
	}
	if strings.HasPrefix(s, "*") {
		a, b := e.marks['<'], e.marks['>']
		return exRange{from: a.Line, to: b.Line, given: 2}, s[1:], nil
	}
	var addrs []int
	for {
		s = strings.TrimLeft(s, " \t")
		n, rest, ok, err := e.parseAddress(v, s, cur)
		if err != nil {
			return r, s, err
		}
		if !ok {
			if len(addrs) > 0 && (strings.HasPrefix(s, ",") || strings.HasPrefix(s, ";")) {
				addrs = append(addrs, cur)
			}
			break
		}
		addrs = append(addrs, n)
		s = rest
		if strings.HasPrefix(s, ",") {
			s = s[1:]
			continue
		}
		if strings.HasPrefix(s, ";") {
			s = s[1:]
			cur = n
			if n >= 0 {
				e.cur.Line = min(n, v.lines()-1)
			}
			continue
		}
		break
	}
	switch len(addrs) {
	case 0:
	case 1:
		r = exRange{from: addrs[0], to: addrs[0], given: 1}
	default:
		r = exRange{from: addrs[len(addrs)-2], to: addrs[len(addrs)-1], given: 2}
	}
	return r, s, nil
}

// parseAddress reads one address and its offsets.
func (e *Engine) parseAddress(v *view, s string, cur int) (int, string, bool, error) {
	n := cur
	got := false
	switch {
	case s == "":
		return 0, s, false, nil
	case s[0] >= '0' && s[0] <= '9':
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		x, _ := strconv.Atoi(s[:i])
		n, s, got = x-1, s[i:], true
	case s[0] == '.':
		n, s, got = cur, s[1:], true
	case s[0] == '$':
		n, s, got = v.lines()-1, s[1:], true
	case s[0] == '\'' && len(s) > 1:
		p, ok := e.markPos(rune(s[1]))
		if !ok {
			return 0, s, false, exError("E20: Mark not set")
		}
		n, s, got = p.Line, s[2:], true
	case s[0] == '/' || s[0] == '?':
		l, rest, err := e.patternAddress(v, s, cur)
		if err != nil {
			return 0, s, false, err
		}
		n, s, got = l, rest, true
		for len(s) > 0 && (s[0] == '/' || s[0] == '?') {
			l, rest, err := e.patternAddress(v, s, n)
			if err != nil {
				return 0, s, false, err
			}
			n, s = l, rest
		}
	case false:
		pat, rest, _ := parseSearch(s[1:], s[0])
		if pat == "" {
			pat = e.search.pat
		}
		re, err := e.compile(pat)
		if err != nil {
			return 0, s, false, err
		}
		e.search = lastSearch{pat: pat, fwd: s[0] == '/', set: true, subPat: e.search.subPat}
		from := Pos{cur, v.lineLen(cur)}
		if s[0] == '?' {
			from = Pos{cur, 0}
		}
		m, ok, _ := e.findPattern(v, re, from, s[0] == '/', 1, false)
		if !ok || (s[0] == '/' && m.Start.Line == cur) || (s[0] == '?' && m.Start.Line == cur) {
			// The search starts on the line after (or before) the cursor line.
			m2, ok2 := e.searchLine(v, re, cur, s[0] == '/')
			if !ok2 {
				return 0, s, false, exError("E486: Pattern not found: " + pat)
			}
			n = m2
		} else {
			n = m.Start.Line
		}
		s, got = rest, true
	case s[0] == '\\' && len(s) > 1 && (s[1] == '/' || s[1] == '?' || s[1] == '&'):
		if !e.search.set {
			return 0, s, false, exError("E35: No previous regular expression")
		}
		re, err := e.compile(e.search.pat)
		if err != nil {
			return 0, s, false, err
		}
		l, ok := e.searchLine(v, re, cur, s[1] != '?')
		if !ok {
			return 0, s, false, exError("E486: Pattern not found: " + e.search.pat)
		}
		n, s, got = l, s[2:], true
	}
	for len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		sign := 1
		if s[0] == '-' {
			sign = -1
		}
		i := 1
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		d := 1
		if i > 1 {
			d, _ = strconv.Atoi(s[1:i])
		}
		n += sign * d
		s = s[i:]
		got = true
	}
	return n, s, got, nil
}

// patternAddress is a /pat/ or ?pat? address: the next (or previous) line from line that matches.
func (e *Engine) patternAddress(v *view, s string, line int) (int, string, error) {
	pat, rest, _ := parseSearch(s[1:], s[0])
	if pat == "" {
		pat = e.search.pat
	}
	re, err := e.compile(pat)
	if err != nil {
		return 0, s, err
	}
	e.search = lastSearch{pat: pat, fwd: s[0] == '/', set: true, subPat: e.search.subPat}
	if line < 0 {
		// After "0;" the search starts before the first line, so the first line can match.
		if _, ok := re.Exec(input{v}, 0, 0, e.regexState(v)); ok && s[0] == '/' {
			return 0, rest, nil
		}
		line = 0
	}
	l, ok := e.searchLine(v, re, line, s[0] == '/')
	if !ok {
		return 0, s, exError("E486: Pattern not found: " + pat)
	}
	return l, rest, nil
}

// searchLine finds the first line after (or before) line that has a match, wrapping around.
func (e *Engine) searchLine(v *view, re *vimregex.Regex, line int, fwd bool) (int, bool) {
	n := v.lines()
	st := e.regexState(v)
	for i := 1; i <= n; i++ {
		l := line + i
		if !fwd {
			l = line - i
		}
		if l < 0 || l >= n {
			if !e.opts.WrapScan {
				return 0, false
			}
			l = (l%n + n) % n
		}
		if _, ok := re.Exec(input{v}, l, 0, st); ok {
			return l, true
		}
	}
	return 0, false
}

// regAndCount reads an optional register name and count after a command, as :d and :y take them.
func regAndCount(args string) (rune, int, error) {
	args = strings.TrimSpace(args)
	var reg rune
	if args != "" && !unicode.IsDigit(rune(args[0])) {
		reg = rune(args[0])
		args = strings.TrimSpace(args[1:])
	}
	if args == "" {
		return reg, 0, nil
	}
	n, err := strconv.Atoi(args)
	if err != nil || n <= 0 {
		return 0, 0, exError("E488: Trailing characters: " + args)
	}
	return reg, n, nil
}

// withCount makes a range of count lines from the range's last line, as ":d 3" does.
func withCount(v *view, r exRange, count int) exRange {
	if count > 0 {
		r.from = r.to
		r.to = min(r.from+count-1, v.lines()-1)
	}
	return r
}

func (e *Engine) report(n int, msg string) {
	if e.inGlobal {
		return
	}
	if n > e.opts.Report {
		e.echo(msg)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (e *Engine) exDelete(v *view, r exRange, args string) error {
	reg, count, err := regAndCount(args)
	if err != nil {
		return err
	}
	r = withCount(v, r, count)
	op := oprange{start: Pos{r.from, 0}, end: Pos{r.to, 0}, kind: linewise, visual: true}
	e.deleteRegs(v, op, reg)
	from, to := op.offsets(v)
	if r.to+1 >= v.lines() && r.from > 0 {
		from--
	}
	e.cur = Pos{r.from, 0}
	e.do(e.group, text.Replace{Range: text.Range{Start: from, End: to}})
	nv := e.view()
	line := min(r.from, nv.lines()-1)
	e.cur = Pos{line, nv.firstNonBlank(line)}
	e.setWant(nv)
	n := r.to - r.from + 1
	e.report(n, fmt.Sprintf("%d fewer lines", n))
	return nil
}

func (e *Engine) exYank(v *view, r exRange, args string) error {
	reg, count, err := regAndCount(args)
	if err != nil {
		return err
	}
	r = withCount(v, r, count)
	op := oprange{start: Pos{r.from, 0}, end: Pos{r.to, 0}, kind: linewise}
	e.yank(v, op, reg)
	n := r.to - r.from + 1
	e.report(n, fmt.Sprintf("%d lines yanked", n))
	return nil
}

// target address for :m, :t and :pu: the line after which text goes, -1 for before the first line.
func (e *Engine) destination(v *view, args string) (int, error) {
	args = strings.TrimSpace(args)
	if args == "" {
		return 0, exError("E14: Invalid address")
	}
	if args == "0" {
		return -1, nil
	}
	n, rest, ok, err := e.parseAddress(v, args, e.cur.Line)
	if err != nil {
		return 0, err
	}
	if !ok || strings.TrimSpace(rest) != "" {
		return 0, exError("E14: Invalid address")
	}
	if n < -1 || n >= v.lines() {
		return 0, exError("E16: Invalid range")
	}
	return n, nil
}

func linesText(v *view, from, to int) string {
	var b strings.Builder
	for l := from; l <= to; l++ {
		b.Write(v.line(l))
		b.WriteByte('\n')
	}
	return b.String()
}

// insertLines puts whole lines after line `after` (-1: at the top).
func (e *Engine) insertLines(v *view, after int, body string) {
	if after+1 < v.lines() {
		at := v.s.LineStart(after + 1)
		e.do(e.group, text.Replace{Range: text.Range{Start: at, End: at}, Text: []byte(body)})
		return
	}
	e.do(e.group, text.Replace{Range: text.Range{Start: v.s.Len(), End: v.s.Len()}, Text: []byte("\n" + strings.TrimSuffix(body, "\n"))})
}

func (e *Engine) deleteLines(v *view, from, to int) {
	op := oprange{start: Pos{from, 0}, end: Pos{to, 0}, kind: linewise}
	a, b := op.offsets(v)
	if to+1 >= v.lines() && from > 0 {
		a--
	}
	e.do(e.group, text.Replace{Range: text.Range{Start: a, End: b}})
}

func (e *Engine) exMove(v *view, r exRange, args string) error {
	dest, err := e.destination(v, args)
	if err != nil {
		return err
	}
	if dest >= r.from && dest < r.to {
		return exError("E134: Cannot move a range of lines into itself")
	}
	n := r.to - r.from + 1
	if dest == r.to || dest == r.from-1 {
		e.cur = Pos{dest, 0}
		if dest < r.from {
			e.cur.Line = r.to
		}
		e.report(n, fmt.Sprintf("%d lines moved", n))
		return nil
	}
	body := linesText(v, r.from, r.to)
	e.cur = Pos{r.from, 0}
	if dest > r.to {
		e.insertLines(v, dest, body)
		e.deleteLines(e.view(), r.from, r.to)
		e.cur = Pos{dest, 0}
	} else {
		e.deleteLines(v, r.from, r.to)
		e.insertLines(e.view(), dest, body)
		e.cur = Pos{dest + n, 0}
	}
	nv := e.view()
	e.cur.Col = nv.firstNonBlank(e.cur.Line)
	e.setWant(nv)
	e.report(n, fmt.Sprintf("%d lines moved", n))
	return nil
}

func (e *Engine) exCopy(v *view, r exRange, args string) error {
	dest, err := e.destination(v, args)
	if err != nil {
		return err
	}
	body := linesText(v, r.from, r.to)
	e.insertLines(v, dest, body)
	n := r.to - r.from + 1
	nv := e.view()
	e.cur.Line = dest + n
	e.cur.Col = nv.colAt(e.cur.Line, e.want, false)
	e.setWant(nv)
	return nil
}

func (e *Engine) exJoin(v *view, r exRange, args string, bang bool) error {
	_, count, err := regAndCount(args)
	if err != nil {
		return err
	}
	from, to := r.from, r.to
	if count > 0 {
		from = r.to
		to = r.to + count - 1
	} else if r.given < 2 {
		to = from + 1
	}
	if from == to {
		e.cur = Pos{from, v.firstNonBlank(from)}
		return nil
	}
	to = min(to, v.lines()-1)
	if from >= to {
		return nil
	}
	e.cur = Pos{from, 0}
	e.join(v, to-from+1, !bang)
	nv := e.view()
	e.cur.Col = nv.firstNonBlank(e.cur.Line)
	e.setWant(nv)
	return nil
}

func (e *Engine) exShift(v *view, r exRange, dir, args string) error {
	times := 1
	for strings.HasPrefix(args, dir) {
		times++
		args = args[1:]
	}
	_, count, err := regAndCount(args)
	if err != nil {
		return err
	}
	r = withCount(v, r, count)
	e.cur = Pos{r.from, 0}
	e.shift(v, oprange{start: Pos{r.from, 0}, end: Pos{r.to, 0}, kind: linewise}, dir == ">", times)
	nv := e.view()
	e.cur = Pos{r.to, nv.colAt(r.to, nv.vcol(r.from, nv.firstNonBlank(r.from)), false)}
	e.setWant(nv)
	n := r.to - r.from + 1
	e.report(n, fmt.Sprintf("%d lines %sed %s", n, dir, plural(times, "time", "times")))
	return nil
}

func (e *Engine) exNormal(v *view, r exRange, args string) error {
	keys := parseKeys(strings.TrimLeft(args, " "))
	lines := make([]int, 0, r.to-r.from+1)
	for l := r.from; l <= r.to; l++ {
		lines = append(lines, l)
	}
	run := func() {
		for _, k := range keys {
			e.Key(k)
		}
		for e.mode != Normal || e.cmdline != nil || len(e.pend) > 0 || e.visual != nil {
			before := e.mode
			e.Key("<Esc>")
			if e.mode == before && e.cmdline == nil && len(e.pend) == 0 && e.visual == nil {
				break
			}
		}
	}
	if r.given == 0 {
		run()
		return nil
	}
	return e.eachMarked(lines, func(l int) error {
		e.cur = Pos{l, 0}
		run()
		return nil
	})
}

// eachMarked runs fn on each line, following the lines through the changes fn makes; a deleted line is skipped.
func (e *Engine) eachMarked(lines []int, fn func(l int) error) error {
	m := &lineMarks{lines: lines}
	e.gstack = append(e.gstack, m)
	defer func() { e.gstack = e.gstack[:len(e.gstack)-1] }()
	for len(m.lines) > 0 {
		l := m.lines[0]
		m.lines = m.lines[1:]
		if l < 0 || l >= e.view().lines() {
			continue
		}
		if err := fn(l); err != nil {
			return err
		}
	}
	return nil
}

// lineMarks are lines :g or :normal still has to visit.
type lineMarks struct{ lines []int }

// parseKeys splits typed text into keys: :normal takes characters literally.
func parseKeys(s string) []string {
	var out []string
	for _, r := range s {
		switch r {
		case '\x1b':
			out = append(out, "<Esc>")
		case '\r':
			out = append(out, "<CR>")
		default:
			out = append(out, string(r))
		}
	}
	return out
}

func (e *Engine) exPut(v *view, r exRange, args string, above bool) error {
	args = strings.TrimSpace(args)
	reg := e.unnamed
	if args != "" {
		reg = rune(args[0])
	}
	val, ok := e.ed.Register(unicode.ToLower(reg))
	if !ok {
		return exError(fmt.Sprintf("E353: Nothing in register %c", reg))
	}
	body := val.Text
	if val.Kind != edit.Linewise {
		body += "\n"
	}
	after := r.to
	if above {
		after = r.to - 1
	}
	after = max(after, -1)
	e.insertLines(v, after, body)
	n := strings.Count(body, "\n")
	nv := e.view()
	line := after + n
	e.cur = Pos{line, nv.firstNonBlank(line)}
	e.setWant(nv)
	return nil
}

// subFlags are a :s command's flags.
type subFlags struct {
	global, ignoreErr, count bool
	ic, noic                 bool
}

func parseSubFlags(s string, prev string) (subFlags, string, string) {
	var f subFlags
	used := ""
	if strings.HasPrefix(s, "&") {
		s = s[1:]
		s = prev + s
	}
	i := 0
	for ; i < len(s); i++ {
		switch s[i] {
		case 'g':
			f.global = !f.global
		case 'e':
			f.ignoreErr = true
		case 'n':
			f.count = true
		case 'i':
			f.ic = true
		case 'I':
			f.noic = true
		case 'c', 'p', '#', 'l', 'r':
		default:
			return f, s[i:], used
		}
		used += string(s[i])
	}
	return f, s[i:], used
}

// exSubstitute is :s. With repeat (:&) the args are only flags and a count.
func (e *Engine) exSubstitute(v *view, r exRange, args string, repeat bool) error {
	var pat, rep, rest string
	useSearch := false
	prevSub := e.lastSub
	if !repeat && args != "" && !unicode.IsLetter(rune(args[0])) && !unicode.IsDigit(rune(args[0])) && args[0] != '"' && args[0] != '|' && args[0] != ' ' && args[0] != '&' {
		delim := args[0]
		p, after, ok := parseSearch(args[1:], delim)
		pat = p
		if ok {
			rp, after2, _ := cutUnescaped(after, delim)
			rep, rest = rp, after2
		}
		if pat == "" {
			if !e.search.set {
				return exError("E35: No previous regular expression")
			}
			pat = e.search.pat
		}
		prevSub = e.lastSub
		rep = vimregex.Tilde(rep, e.lastSub, e.opts.Magic)
		e.lastSub = rep
		e.search.subPat = pat
		e.search.pat, e.search.set = pat, true
	} else {
		if e.search.subPat == "" && !e.search.set {
			return exError("E35: No previous regular expression")
		}
		pat = e.search.subPat
		if useSearch || strings.HasPrefix(args, "r") {
			pat = e.search.pat
			args = strings.TrimPrefix(args, "r")
		}
		rep = e.lastSub
		rest = args
	}
	rest = strings.TrimLeft(rest, " ")
	flags, rest, used := parseSubFlags(rest, e.lastSubFlags)
	if !repeat || strings.HasPrefix(args, "&") {
		e.lastSubFlags = used
	}
	rest = strings.TrimSpace(rest)
	if rest != "" {
		n, err := strconv.Atoi(rest)
		if err != nil {
			return exError("E488: Trailing characters: " + rest)
		}
		if n <= 0 {
			return exError("E939: Positive count required")
		}
		r = withCount(v, r, n)
	}
	o := vimregex.Options{IgnoreCase: e.opts.IgnoreCase, SmartCase: e.opts.SmartCase, Magic: e.opts.Magic, LastSub: prevSub}
	if flags.ic {
		o.IgnoreCase, o.SmartCase = true, false
	}
	if flags.noic {
		o.IgnoreCase = false
	}
	re, err := vimregex.Compile(pat, o)
	if err != nil {
		return err
	}
	return e.substitute(v, r, re, rep, flags, pat)
}

// cutUnescaped splits s at the first delim not escaped by a backslash.
func cutUnescaped(s string, delim byte) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			continue
		}
		if s[i] == delim {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func (e *Engine) exTilde(v *view, r exRange, args string) error {
	if !e.search.set {
		return exError("E35: No previous regular expression")
	}
	e.search.subPat = e.search.pat
	return e.exSubstitute(v, r, args, true)
}

// substitute runs a compiled :s over the range, as vim's do_sub(): the matches in a line are found in the
// line as it was and the new line is built from them; after a match that joins lines, the search carries on
// in the joined line.
func (e *Engine) substitute(v *view, r exRange, re *vimregex.Regex, rep string, f subFlags, pat string) error {
	subs, lines := 0, 0
	lastLine := -1
	orig := e.cur
	end := r.to
	for line := r.from; line <= end && line < v.lines(); line++ {
		col, copied, prevEnd := 0, 0, -1
		var out strings.Builder
		pending := false
		lineHad := false
		for {
			in := input{v}
			m, ok := re.Exec(in, line, col, e.regexState(v))
			if !ok || m.Start.Line != line {
				break
			}
			empty := m.Start == m.End
			if empty && m.Start.Col == prevEnd {
				if col >= v.lineLen(line) {
					break
				}
				col = nextCol(v, line, col)
				prevEnd = -1
				continue
			}
			subs++
			if !lineHad {
				lineHad = true
				lines++
			}
			endsBuffer := m.End.Line >= v.lines()
			joined := min(m.End.Line, v.lines()-1) - line
			if f.count {
				if joined > 0 || endsBuffer || !f.global {
					break
				}
				col = nextColAfter(v, m, line)
				prevEnd = m.End.Col
				if col > v.lineLen(line) {
					break
				}
				continue
			}
			repl := vimregex.Expand(rep, m, in, e.opts.Magic)
			l := v.line(line)
			if joined > 0 || endsBuffer {
				// Put in what's built so far plus this match, then search on in the line it leaves.
				out.Write(l[copied:m.Start.Col])
				out.WriteString(repl)
				start := v.s.LineStart(line)
				to := v.s.Len()
				if !endsBuffer {
					to = v.offset(toPos(m.End))
				}
				e.cur = Pos{line, 0}
				e.do(e.group, text.Replace{Range: text.Range{Start: start, End: to}, Text: []byte(out.String())})
				v = e.view()
				after := v.pos(start + out.Len())
				lastLine = after.Line
				end += strings.Count(out.String(), "\n") - strings.Count(string(l), "\n") - joined
				out.Reset()
				pending = false
				if endsBuffer {
					end = -1
					break
				}
				// As vim's do_again: an empty match right where the replacement ends doesn't count.
				line, col, copied, prevEnd = after.Line, after.Col, after.Col, after.Col
				lineHad = false
				// Keep what's before the replacement as it is now.
				out.Write(v.line(line)[:col])
				continue
			}
			pending = true
			out.Write(l[copied:m.Start.Col])
			out.WriteString(repl)
			copied = m.End.Col
			col = m.End.Col
			prevEnd = col
			if empty {
				if col >= len(l) {
					break
				}
				_, n := v.char(Pos{line, col})
				out.Write(l[col : col+n])
				copied = col + n
				col = copied
				if col >= len(l) {
					break
				}
			}
			if !f.global {
				break
			}
		}
		if pending {
			lastLine = line
			l := v.line(line)
			out.Write(l[copied:])
			start := v.s.LineStart(line)
			e.cur = Pos{line, 0}
			e.do(e.group, text.Replace{Range: text.Range{Start: start, End: start + len(l)}, Text: []byte(out.String())})
			added := strings.Count(out.String(), "\n")
			v = e.view()
			line += added
			end += added
			lastLine = line
		}
	}
	if subs == 0 {
		if f.ignoreErr || e.inGlobal {
			return nil
		}
		return exError("E486: Pattern not found: " + pat)
	}
	if f.count {
		e.echo(fmt.Sprintf("%s on %s", plural(subs, "match", "matches"), plural(lines, "line", "lines")))
		return nil
	}
	if !e.inGlobal {
		e.cur = orig
		e.setPcmark()
	}
	lastLine = min(max(lastLine, 0), v.lines()-1)
	e.cur = Pos{lastLine, v.firstNonBlank(lastLine)}
	e.setWant(v)
	if !e.inGlobal && (subs > e.opts.Report || lines > e.opts.Report) {
		e.echo(fmt.Sprintf("%s on %s", plural(subs, "substitution", "substitutions"), plural(lines, "line", "lines")))
	}
	return nil
}

func nextColAfter(v *view, m vimregex.Match, line int) int {
	if m.End.Line > line {
		return v.lineLen(line) + 1
	}
	if m.End == m.Start {
		return nextCol(v, line, m.Start.Col)
	}
	return m.End.Col
}

// exGlobal is :g and :v: mark the lines that match (or don't), then run the command on each that's left.
func (e *Engine) exGlobal(v *view, r exRange, args string, invert, whole bool) error {
	if whole && e.inGlobal {
		whole = false
	}
	if whole {
		r = exRange{from: 0, to: v.lines() - 1}
	}
	if args == "" {
		return exError("E35: No previous regular expression")
	}
	delim := args[0]
	pat, cmdText, _ := parseSearch(args[1:], delim)
	if pat == "" {
		if !e.search.set {
			return exError("E35: No previous regular expression")
		}
		pat = e.search.pat
	}
	re, err := e.compile(pat)
	if err != nil {
		return err
	}
	e.search = lastSearch{pat: pat, fwd: true, set: true, subPat: e.search.subPat}
	var marked []int
	in := input{v}
	st := e.regexState(v)
	for l := r.from; l <= r.to; l++ {
		_, ok := re.Exec(in, l, 0, st)
		if ok != invert {
			marked = append(marked, l)
		}
	}
	if len(marked) == 0 {
		if invert {
			e.echo("Pattern found in every line: " + pat)
		} else {
			e.echo("Pattern not found: " + pat)
		}
		return nil
	}
	cmdText = strings.TrimLeft(cmdText, " ")
	if cmdText == "" {
		cmdText = "p"
	}
	if strings.HasPrefix(cmdText, "norm") {
		// :normal from :g runs once per marked line, so hand it the lines directly.
		name, a := splitCommand(cmdText)
		if name == "normal" {
			keys := parseKeys(strings.TrimLeft(strings.TrimPrefix(a, "!"), " "))
			return e.eachMarked(marked, func(l int) error {
				e.cur = Pos{l, 0}
				for _, k := range keys {
					e.Key(k)
				}
				for i := 0; i < 3 && (e.mode != Normal || e.cmdline != nil || e.visual != nil); i++ {
					e.Key("<Esc>")
				}
				return nil
			})
		}
	}
	before := v.lines()
	outer := e.inGlobal
	e.inGlobal = true
	err = e.eachMarked(marked, func(l int) error {
		e.cur = Pos{l, 0}
		return e.exec1(e.view(), cmdText)
	})
	e.inGlobal = outer
	if err != nil {
		return err
	}
	if !outer {
		after := e.view().lines()
		switch {
		case before-after > e.opts.Report:
			e.echo(fmt.Sprintf("%d fewer lines", before-after))
		case after-before > e.opts.Report:
			e.echo(fmt.Sprintf("%d more lines", after-before))
		}
	}
	return nil
}

func (e *Engine) exSet(args string) error {
	for _, a := range strings.Fields(args) {
		name, val, hasVal := strings.Cut(a, "=")
		on := true
		if !hasVal && strings.HasPrefix(name, "no") {
			on = false
			name = name[2:]
		}
		switch name {
		case "sw", "shiftwidth":
			n, err := strconv.Atoi(val)
			if err != nil {
				return exError("E521: Number required after =: " + a)
			}
			e.opts.ShiftWidth = n
		case "et", "expandtab":
			e.opts.ExpandTab = on
		case "ic", "ignorecase":
			e.opts.IgnoreCase = on
		case "scs", "smartcase":
			e.opts.SmartCase = on
		case "ws", "wrapscan":
			e.opts.WrapScan = on
		case "magic":
			e.opts.Magic = on
		case "report":
			n, err := strconv.Atoi(val)
			if err != nil {
				return exError("E521: Number required after =: " + a)
			}
			e.opts.Report = n
		default:
			return exError("E518: Unknown option: " + name)
		}
	}
	return nil
}

// confirmation is a yes/no question waiting for the next key.
type confirmation struct {
	prompt string
	yes    func()
}
