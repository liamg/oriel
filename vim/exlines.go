package vim

import (
	"strings"
	"unicode"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/text"
)

func (e *Engine) report(n int, msg edit.Notice) {
	if e.inGlobal {
		return
	}
	if n > e.opts.Report {
		e.notify(msg)
	}
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
	e.report(n, msgFewerLines.with(n))
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
	e.report(n, msgYanked.with(n))
	return nil
}

// target address for :m, :t and :pu: the line after which text goes, -1 for before the first line.
func (e *Engine) destination(v *view, args string) (int, error) {
	args = strings.TrimSpace(args)
	if args == "" {
		return 0, errInvalidAddress.with()
	}
	if args == "0" {
		return -1, nil
	}
	n, rest, ok, err := e.parseAddress(v, args, e.cur.Line)
	if err != nil {
		return 0, err
	}
	if !ok || strings.TrimSpace(rest) != "" {
		return 0, errInvalidAddress.with()
	}
	if n < -1 || n >= v.lines() {
		return 0, errInvalidRange.with()
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
	if from == 0 && to+1 >= v.lines() {
		e.buffer.noLines = true
	}
}

func (e *Engine) exMove(v *view, r exRange, args string) error {
	dest, err := e.destination(v, args)
	if err != nil {
		return err
	}
	if dest >= r.from && dest < r.to {
		return errMoveIntoItself.with()
	}
	n := r.to - r.from + 1
	if dest == r.to || dest == r.from-1 {
		e.cur = Pos{dest, 0}
		if dest < r.from {
			e.cur.Line = r.to
		}
		e.report(n, msgMoved.with(n))
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
	e.report(n, msgMoved.with(n))
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
	e.report(n, msgShifted.with(n, dir, times))
	return nil
}

func (e *Engine) exNormal(v *view, r exRange, args string, bang bool) error {
	keys := rawToKeys(strings.TrimLeft(args, " "))
	lines := make([]int, 0, r.to-r.from+1)
	for l := r.from; l <= r.to; l++ {
		lines = append(lines, l)
	}
	run := func() {
		e.feed(func() {
			if bang {
				e.noremap++
				defer func() { e.noremap-- }()
			}
			saved := e.mapBuf
			e.mapBuf = nil
			for _, k := range keys {
				if e.failed {
					break
				}
				e.Key(k)
			}
			e.resolveMaps(true)
			e.mapBuf = saved
		})
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
	var err error
	e.feed(func() {
		err = e.eachMarked(lines, func(l int) error {
			e.cur = Pos{l, 0}
			e.failed = false
			run()
			return nil
		})
	})
	return err
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

func (e *Engine) exPut(v *view, r exRange, args string, above bool) error {
	args = strings.TrimSpace(args)
	reg := e.unnamed
	if args != "" {
		reg = rune(args[0])
	}
	val, ok := e.ed.Register(unicode.ToLower(reg))
	if !ok {
		return errEmptyRegister.with(string(reg))
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

// exGlobal is :g and :v: mark the lines that match (or don't), then run the command on each that's left.
func (e *Engine) exGlobal(v *view, r exRange, args string, invert, whole bool) error {
	if whole && e.inGlobal {
		whole = false
	}
	if whole {
		r = exRange{from: 0, to: v.lines() - 1}
	}
	if args == "" {
		return errNoPrevPattern.with()
	}
	delim := args[0]
	pat, cmdText, _ := parseSearch(args[1:], delim)
	if pat == "" {
		if !e.search.set {
			return errNoPrevPattern.with()
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
			e.notify(msgEveryLine.with(pat))
		} else {
			e.notify(msgNoLine.with(pat))
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
			keys := rawToKeys(strings.TrimLeft(strings.TrimPrefix(a, "!"), " "))
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
			e.notify(msgFewerLines.with(before - after))
		case after-before > e.opts.Report:
			e.notify(msgMoreLines.with(after - before))
		}
	}
	return nil
}
