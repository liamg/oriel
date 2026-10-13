package vim

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/text"
)

// oprange is the text an operator works on, after vim's adjustments.
type oprange struct {
	start, end Pos // end is exclusive for charwise ranges, the last line for linewise ones
	kind       motionKind
	useRegOne  bool // the motion always fills "1, even within a line
	curStart   Pos  // where the cursor goes for a yank
	adjusted   bool
	visual     bool // from a Visual area: no exclusive-motion rules
	shiftBy    int  // how many shiftwidths > and < move
}

func (r oprange) lines() int { return r.end.Line - r.start.Line + 1 }

func less(a, b Pos) bool { return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col }

// regOneMotions always put a deletion in "1 (vi compatible), even inside one line.
var regOneMotions = map[string]bool{"%": true, "(": true, ")": true, "`": true, "/": true, "?": true, "n": true, "N": true, "{": true, "}": true}

func (e *Engine) operator(v *view, c cmd) {
	oldWant := e.want
	var r oprange
	switch {
	case c.line:
		n := c.count1()
		if n > 1 && e.cur.Line >= v.lines()-1 {
			e.rec = nil
			return
		}
		last, _ := foldStep(v, e.cur.Line, n-1)
		start := e.cur
		if top, _, ok := v.folded(start.Line); ok {
			start = Pos{top, 0}
		}
		_, last, _ = v.folded(last)
		r = oprange{start: start, end: Pos{last, 0}, kind: linewise, curStart: start}
	case c.obj != "":
		s, ok := objects[c.obj](e, v, c.count1(), c.obj[0] == 'i')
		if !ok {
			e.rec = nil
			e.failed = true
			return
		}
		if s.empty {
			e.cur = s.start
			e.rec = nil
			if c.op == "c" {
				e.startInsert(v, c, "c")
			}
			return
		}
		if s.noAdjust {
			r = oprange{start: s.start, end: s.end, kind: exclusive, curStart: s.start}
		} else {
			r = e.adjust(v, s.start, s.end, s.kind, false)
		}
	default:
		t, ok := motions[c.key](e, v, motionCtx{count: c.count, arg: c.arg, op: c.op, key: c.key, search: c.searchTarget})
		if !ok {
			e.rec = nil
			e.failed = true
			return
		}
		if t.jump {
			e.setPcmark()
		}
		to := t.pos
		if t.keepCol {
			to.Col = v.colAt(to.Line, e.want, false)
		}
		if t.wantEnd {
			e.want = maxCol
		}
		from := e.cur
		from, to = includeFolds(v, from, to, t.kind)
		r = e.adjust(v, from, to, t.kind, true)
		r.useRegOne = regOneMotions[c.key]
	}
	e.applyOp(v, c, r, oldWant)
}

// applyOp runs operator c.op over r.
func (e *Engine) applyOp(v *view, c cmd, r oprange, oldWant int) {
	switch c.op {
	case "y":
		e.yank(v, r, c.reg)
		e.cur = r.curStart
		e.clamp(v)
		e.setWant(v)
	case "d":
		e.delete(v, r, c.reg)
		if r.kind == linewise && !r.adjusted {
			e.cur.Col = e.view().colAt(e.cur.Line, oldWant, false)
			e.want = oldWant
		}
	case "c":
		e.change(v, r, c)
	case "<", ">":
		e.shift(v, r, c.op == ">", max(r.shiftBy, 1))
		e.cur.Line = r.start.Line
		e.cur.Col = e.view().colAt(e.cur.Line, oldWant, false)
		e.want = oldWant
	case "g~", "gu", "gU", "g?":
		e.changeCase(v, r, c.op)
	case "zf":
		end := r.end.Line
		if r.kind != linewise && r.end.Col == 0 && end > r.start.Line {
			end--
		}
		e.cur = r.start
		e.createFold(r.start.Line, end)
		e.rec = nil
	}
}

// adjust orders from and to into a range and applies vim's rules for exclusive motions.
func (e *Engine) adjust(v *view, from, to Pos, kind motionKind, fromCursor bool) oprange {
	start, end := from, to
	if less(to, from) {
		start, end = to, from
	}
	r := oprange{start: start, end: end, kind: kind, curStart: start}
	if kind == linewise {
		return r
	}
	if kind == exclusive && end.Col == 0 && end.Line > start.Line {
		// *exclusive-linewise*: an exclusive motion that ends in column 0 ends on the line before.
		r.adjusted = true
		r.end.Line--
		if v.inindent(start, 0) {
			r.kind = linewise
			return r
		}
		r.end.Col = v.lineLen(r.end.Line)
		if r.end.Col > 0 {
			r.end.Col = v.lastCol(r.end.Line)
			kind = inclusive
		}
	}
	if kind == inclusive {
		_, n := v.char(r.end)
		r.end.Col += n
	}
	r.kind = exclusive
	return r
}

// offsets returns the byte range an oprange covers. Linewise ranges include the newline after the last
// line, or the one before the first when they run to the end of the buffer.
func (r oprange) offsets(v *view) (int, int) {
	if r.kind != linewise {
		return v.offset(r.start), v.offset(r.end)
	}
	from := v.s.LineStart(r.start.Line)
	if r.end.Line+1 < v.lines() {
		return from, v.s.LineStart(r.end.Line + 1)
	}
	return from, v.s.Len()
}

// regText is what an oprange puts in a register.
func (r oprange) regText(v *view) edit.Register {
	if r.kind == linewise {
		var b strings.Builder
		for l := r.start.Line; l <= r.end.Line; l++ {
			b.Write(v.line(l))
			b.WriteByte('\n')
		}
		return edit.Register{Text: b.String(), Kind: edit.Linewise}
	}
	from, to := r.offsets(v)
	return edit.Register{Text: string(v.s.Slice(text.Range{Start: from, End: to})), Kind: edit.Charwise}
}

func validReg(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("\"-_.:%#/+*=", r)
}

// setReg writes a register, appending for an uppercase name, and points "" at it.
func (e *Engine) setReg(name rune, v edit.Register) {
	if name >= 'A' && name <= 'Z' {
		name = unicode.ToLower(name)
		if old, ok := e.ed.Register(name); ok {
			v = appendReg(old, v)
		}
	}
	e.ed.SetRegister(name, v)
	e.unnamed = name
}

func appendReg(old, add edit.Register) edit.Register {
	switch {
	case add.Kind == edit.Linewise && old.Kind != edit.Linewise:
		return edit.Register{Text: old.Text + "\n" + add.Text, Kind: edit.Linewise}
	case old.Kind == edit.Linewise:
		t := add.Text
		if add.Kind != edit.Linewise {
			t += "\n"
		}
		return edit.Register{Text: old.Text + t, Kind: edit.Linewise}
	}
	return edit.Register{Text: old.Text + add.Text, Kind: old.Kind}
}

func (e *Engine) yank(v *view, r oprange, reg rune) {
	if reg == '_' {
		return
	}
	val := r.regText(v)
	if reg == 0 || reg == '"' {
		reg = '0'
	}
	e.setReg(reg, val)
	e.setOpMarks(v, r)
}

// deleteRegs fills the registers a deletion writes.
func (e *Engine) deleteRegs(v *view, r oprange, reg rune) {
	if reg == '_' {
		return
	}
	val := r.regText(v)
	if reg == '"' {
		reg = 0
	}
	if reg != 0 {
		e.setReg(reg, val)
	}
	if r.kind == linewise || r.lines() > 1 || r.useRegOne {
		for n := '9'; n > '1'; n-- {
			if prev, ok := e.ed.Register(n - 1); ok {
				e.ed.SetRegister(n, prev)
			}
		}
		e.ed.SetRegister('1', val)
		if reg == 0 || reg >= 'a' && reg <= 'z' {
			e.unnamed = '1'
		}
		if reg != 0 {
			e.unnamed = unicode.ToLower(reg)
		}
	}
	if reg == 0 && r.kind != linewise && r.lines() == 1 {
		e.setReg('-', val)
	}
}

func (e *Engine) delete(v *view, r oprange, reg rune) {
	if r.kind == exclusive && r.lines() > 1 && !r.visual {
		// A charwise delete that leaves only white space and starts in the indent becomes linewise.
		rest := v.line(r.end.Line)[min(r.end.Col, v.lineLen(r.end.Line)):]
		if len(strings.TrimLeft(string(rest), " \t")) == 0 && v.inindent(r.start, 0) {
			r.kind = linewise
		}
	}
	from, to := r.offsets(v)
	if from == to && r.kind != linewise {
		e.cur = r.start
		return
	}
	e.deleteRegs(v, r, reg)
	if r.kind == linewise && r.end.Line+1 >= v.lines() && r.start.Line > 0 {
		from--
	}
	e.cur = r.start
	e.do(e.group, text.Replace{Range: text.Range{Start: from, End: to}})
	nv := e.view()
	if r.kind == linewise && r.start.Line == 0 && r.end.Line+1 >= v.lines() {
		e.buffer.noLines = true
	}
	if r.kind == linewise {
		e.cur = Pos{min(r.start.Line, nv.lines()-1), 0}
		e.cur.Col = nv.firstNonBlank(e.cur.Line)
		e.marks['['] = Pos{e.cur.Line, 0}
		e.marks[']'] = Pos{e.cur.Line, 0}
	} else {
		e.cur = r.start
		e.marks['['] = r.start
		e.marks[']'] = r.start
	}
	e.clamp(nv)
	e.setWant(nv)
}

func (e *Engine) change(v *view, r oprange, c cmd) {
	if r.kind == linewise {
		e.deleteRegs(v, r, c.reg)
		indent := ""
		l := v.line(r.start.Line)
		indent = string(l[:v.indentEnd(r.start.Line)])
		from, to := r.offsets(v)
		if r.end.Line+1 < v.lines() {
			to--
		}
		e.cur = Pos{r.start.Line, len(indent)}
		e.do(e.group, text.Replace{Range: text.Range{Start: from, End: to}, Text: []byte(indent)})
		e.cur = Pos{r.start.Line, len(indent)}
		e.startInsert(e.view(), c, "c")
		if indent != "" {
			e.ins.aiLine = e.cur.Line
		}
		return
	}
	e.deleteRegs(v, r, c.reg)
	from, to := r.offsets(v)
	e.cur = r.start
	if from != to {
		e.do(e.group, text.Replace{Range: text.Range{Start: from, End: to}})
	}
	e.cur = r.start
	e.startInsert(e.view(), c, "c")
}

func (e *Engine) shift(v *view, r oprange, right bool, amount int) {
	var rs []text.Replace
	last := r.end.Line
	if r.kind != linewise && r.end.Col == 0 && r.end.Line > r.start.Line {
		last--
	}
	for l := r.start.Line; l <= last; l++ {
		if v.lineLen(l) == 0 {
			continue
		}
		ind := v.indentEnd(l)
		width := v.vcol(l, ind)
		sw := e.opts.ShiftWidth
		if right {
			width += sw * amount
		} else {
			width = max(0, width-sw*amount)
		}
		start := v.s.LineStart(l)
		rs = append(rs, text.Replace{Range: text.Range{Start: start, End: start + ind}, Text: []byte(e.makeIndent(width))})
	}
	if len(rs) > 0 {
		e.do(e.group, rs...)
	}
	nv := e.view()
	e.marks['['] = Pos{r.start.Line, 0}
	e.marks[']'] = Pos{last, nv.lineLen(last)}
}

// makeIndent builds width columns of indent: tabs then spaces, or only spaces with 'expandtab'.
func (e *Engine) makeIndent(width int) string {
	if e.opts.ExpandTab {
		return strings.Repeat(" ", width)
	}
	return strings.Repeat("\t", width/tabstop) + strings.Repeat(" ", width%tabstop)
}

func rot13(r rune) rune {
	switch {
	case r >= 'a' && r <= 'z':
		return 'a' + (r-'a'+13)%26
	case r >= 'A' && r <= 'Z':
		return 'A' + (r-'A'+13)%26
	}
	return r
}

// toUpper is unicode.ToUpper, except that ß becomes ẞ as it does in nvim.
func toUpper(r rune) rune {
	if r == 'ß' {
		return 'ẞ'
	}
	return unicode.ToUpper(r)
}

func caseFunc(op string) func(rune) rune {
	switch op {
	case "gu":
		return unicode.ToLower
	case "gU":
		return toUpper
	case "g?":
		return rot13
	}
	return func(r rune) rune {
		if unicode.IsUpper(r) {
			return unicode.ToLower(r)
		}
		return toUpper(r)
	}
}

func (e *Engine) changeCase(v *view, r oprange, op string) {
	from, to := r.offsets(v)
	if r.kind == linewise {
		to = v.s.LineStart(r.end.Line) + v.lineLen(r.end.Line)
	}
	old := v.s.Slice(text.Range{Start: from, End: to})
	nw := []byte(strings.Map(caseFunc(op), string(old)))
	if string(nw) != string(old) {
		e.do(e.group, text.Replace{Range: text.Range{Start: from, End: to}, Text: nw})
	}
	e.cur = r.start
	if r.kind == linewise && (r.lines() == 1 || r.visual) {
		e.cur.Col = 0
	} else if r.kind == linewise {
		e.cur.Col = min(e.cur.Col, e.changeCursor.Col)
		e.cur = e.changeCursor
		e.cur.Line = r.start.Line
	}
	e.marks['['] = r.start
	e.marks[']'] = v.pos(max(from, to-1))
}

func (e *Engine) setOpMarks(v *view, r oprange) {
	e.marks['['] = r.start
	if r.kind == linewise {
		e.marks['['] = Pos{r.start.Line, 0}
		e.marks[']'] = Pos{r.end.Line, maxCol}
		return
	}
	end := r.end
	if end.Col > 0 {
		end.Col = v.prevCol(end.Line, end.Col)
	} else if end.Line > r.start.Line {
		end = Pos{end.Line - 1, v.lineLen(end.Line - 1)}
	}
	e.marks[']'] = end
}

// put is p, P, gp and gP.
func (e *Engine) put(v *view, c cmd) {
	reg := c.reg
	if reg == 0 || reg == '"' {
		reg = e.unnamed
	}
	val, ok := e.ed.Register(unicode.ToLower(reg))
	if reg == '.' {
		val, ok = edit.Register{Text: e.inserted}, e.inserted != ""
	}
	if !ok || reg == 0 {
		name := c.reg
		if name == 0 {
			name = '"'
		}
		e.notify(errEmptyRegister.with(string(name)))
		e.rec = nil
		return
	}
	after := c.key == "p" || c.key == "gp"
	gp := c.key[0] == 'g'
	n := c.count1()
	if val.Kind == edit.Blockwise {
		e.putBlock(v, val, after, gp, n)
		return
	}
	if val.Kind == edit.Linewise {
		body := strings.Repeat(val.Text, n)
		line := e.cur.Line
		if top, end, ok := v.folded(line); ok {
			line = top
			if after {
				line = end
			}
		}
		var at int
		if after {
			line++
		}
		if line >= v.lines() {
			at = v.s.Len()
			body = "\n" + strings.TrimSuffix(body, "\n")
		} else {
			at = v.s.LineStart(line)
		}
		e.do(e.group, text.Replace{Range: text.Range{Start: at, End: at}, Text: []byte(body)})
		nv := e.view()
		added := strings.Count(val.Text, "\n") * n
		e.marks['['] = Pos{line, 0}
		e.marks[']'] = Pos{line + added - 1, nv.lastCol(line + added - 1)}
		if gp {
			e.cur = Pos{min(line+added, nv.lines()-1), 0}
		} else {
			e.cur = Pos{line, nv.firstNonBlank(line)}
		}
		e.clamp(nv)
		e.setWant(nv)
		return
	}
	body := strings.Repeat(val.Text, n)
	at := v.offset(e.cur)
	if after && v.lineLen(e.cur.Line) > 0 {
		_, size := v.char(e.cur)
		at += size
	}
	e.do(e.group, text.Replace{Range: text.Range{Start: at, End: at}, Text: []byte(body)})
	nv := e.view()
	start := nv.pos(at)
	endOff := at + len(body)
	e.marks['['] = start
	e.marks[']'] = nv.pos(max(at, lastRuneStart(nv, endOff)))
	switch {
	case gp:
		e.cur = nv.pos(endOff)
		if e.cur.Col >= nv.lineLen(e.cur.Line) && strings.HasSuffix(body, "\n") {
			e.cur.Col = 0
		}
	case strings.Contains(val.Text, "\n"):
		e.cur = start
	default:
		_, size := utf8.DecodeLastRuneInString(body)
		e.cur = nv.pos(endOff - size)
	}
	if !gp {
		e.clamp(nv)
	}
	e.setWant(nv)
}

// putIndented is ]p and [p: put a linewise register with its indent adjusted to the cursor line's.
func (e *Engine) putIndented(v *view, c cmd) {
	reg := c.reg
	if reg == 0 || reg == '"' {
		reg = e.unnamed
	}
	val, ok := e.ed.Register(unicode.ToLower(reg))
	c2 := c
	c2.key = "P"
	if c.key == "]p" {
		c2.key = "p"
	}
	if !ok || val.Kind != edit.Linewise {
		e.put(v, c2)
		return
	}
	lines := strings.Split(strings.TrimSuffix(val.Text, "\n"), "\n")
	firstInd := indentWidth(lines[0])
	target := v.vcol(e.cur.Line, v.indentEnd(e.cur.Line))
	for i, l := range lines {
		trimmed := strings.TrimLeft(l, " \t")
		w := max(0, indentWidth(l)-firstInd+target)
		if trimmed == "" {
			lines[i] = ""
			continue
		}
		lines[i] = e.makeIndent(w) + trimmed
	}
	saved := val
	e.ed.SetRegister(unicode.ToLower(reg), edit.Register{Text: strings.Join(lines, "\n") + "\n", Kind: edit.Linewise})
	c2.reg = unicode.ToLower(reg)
	e.put(v, c2)
	e.ed.SetRegister(unicode.ToLower(reg), saved)
}

// indentWidth is how many screen columns a line's leading white space takes.
func indentWidth(l string) int {
	w := 0
	for _, r := range l {
		switch r {
		case ' ':
			w++
		case '\t':
			w += tabstop - w%tabstop
		default:
			return w
		}
	}
	return w
}

// lastRuneStart is the offset of the character that ends at off.
func lastRuneStart(v *view, off int) int {
	if off <= 0 {
		return 0
	}
	p := v.pos(off)
	if p.Col == 0 {
		return off - 1
	}
	return off - (p.Col - v.prevCol(p.Line, p.Col))
}

// includeFolds widens an operator's range to whole closed folds at either end, as vim does.
func includeFolds(v *view, from, to Pos, kind motionKind) (Pos, Pos) {
	if less(from, to) {
		if top, _, ok := v.folded(from.Line); ok {
			from = Pos{top, 0}
		}
		if to.Col > 0 || kind != exclusive {
			if _, end, ok := v.folded(to.Line); ok {
				to = Pos{end, v.lineLen(end)}
			}
		}
		return from, to
	}
	if kind == linewise {
		if top, _, ok := v.folded(to.Line); ok {
			to = Pos{top, 0}
		}
		if _, end, ok := v.folded(from.Line); ok {
			from = Pos{end, v.lineLen(end)}
		}
	}
	return from, to
}
