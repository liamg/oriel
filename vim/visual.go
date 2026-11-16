package vim

import (
	"strings"
	"unicode/utf8"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/text"
)

const ctrlV = 0x16

// visualCmds are the keys Visual mode handles itself rather than as motions.
var visualCmds = map[string]bool{
	"<Esc>": true, "<C-c>": true, "v": true, "V": true, "<C-v>": true, "o": true, "O": true, "gv": true,
	"x": true, "X": true, "D": true, "Y": true, "C": true, "S": true, "R": true, "s": true, "r": true,
	"J": true, "gJ": true, "~": true, "u": true, "U": true, "p": true, "P": true, "I": true, "A": true,
	"<Del>": true, ":": true, "<C-a>": true, "<C-x>": true, "g<C-a>": true, "g<C-x>": true,
	"zd": true, "zD": true, "zo": true, "zO": true, "zc": true, "zC": true,
}

// visualArea is a Visual area as gv brings it back.
type visualArea struct {
	start, end Pos
	mode       rune
	wantEnd    bool
}

// visualSize is how much text a Visual operator covered, for repeating it with ".".
type visualSize struct {
	mode    rune
	lines   int
	cols    int // charwise on one line: characters; block: screen columns
	endCol  int // charwise over several lines: the end column
	wantEnd bool
}

func (e *Engine) startVisual(mode rune, count int) {
	if count > 0 && e.lastVisSize != nil {
		// A count reselects the last Visual operation's amount of text, in its mode, multiplied.
		s := *e.lastVisSize
		switch {
		case s.mode == ctrlV:
			s.cols *= count
			s.lines *= count
		case s.mode == 'v' && s.lines == 1:
			s.cols *= count
		default:
			s.lines *= count
		}
		e.restoreArea(e.view(), &s)
		return
	}
	p := e.cur
	e.visual = &p
	e.vmode = mode
	if count > 0 && mode == 'v' {
		v := e.view()
		for range count - 1 {
			if _, n := v.char(e.cur); e.cur.Col+n < v.lineLen(e.cur.Line) {
				e.cur.Col += n
			}
		}
		e.setWant(v)
	}
	if count > 1 && mode == 'V' {
		e.cur.Line = min(e.cur.Line+count-1, e.view().lines()-1)
	}
}

func (e *Engine) reselect() {
	a := e.lastVisual
	if a == nil {
		return
	}
	if e.visual != nil {
		cur := visualArea{start: *e.visual, end: e.cur, mode: e.vmode, wantEnd: e.want == maxCol}
		e.lastVisual = &cur
	}
	v := e.view()
	start, end := a.start, a.end
	start.Line, end.Line = min(start.Line, v.lines()-1), min(end.Line, v.lines()-1)
	e.visual = &start
	e.vmode = a.mode
	e.cur = end
	e.clampVisual(v)
	if a.wantEnd {
		e.want = maxCol
	} else {
		e.setWant(v)
	}
}

// Select makes the Visual area run from start to end, in mode 'v', 'V' or CTRL-V, as the mouse does (term.txt
// *mouse-overview*): Insert or Replace mode ends first, and the cursor goes to end.
func (e *Engine) Select(start, end Pos, mode rune) {
	if e.mode == Insert || e.mode == Replace {
		e.finishInsert()
	}
	v := e.view()
	e.visual = &start
	e.vmode = mode
	e.cur = end
	e.clampVisual(v)
	e.setWant(v)
	e.sync()
}

// Deselect leaves Visual mode, keeping the area for gv, as a click does.
func (e *Engine) Deselect() {
	e.endVisual()
	e.sync()
}

// endVisual leaves Visual mode, remembering the area for gv and the '< and '> marks.
func (e *Engine) endVisual() {
	if e.visual == nil {
		return
	}
	start, end := *e.visual, e.cur
	e.lastVisual = &visualArea{start: start, end: end, mode: e.vmode, wantEnd: e.want == maxCol}
	if less(end, start) {
		start, end = end, start
	}
	if e.vmode == 'V' {
		start.Col, end.Col = 0, maxCol
	}
	e.marks['<'] = start
	e.marks['>'] = end
	e.visual = nil
}

// clampVisual keeps the cursor on a character; after $ it may sit on the end of the line.
func (e *Engine) clampVisual(v *view) {
	e.cur.Line = min(max(e.cur.Line, 0), v.lines()-1)
	n := v.lineLen(e.cur.Line)
	if e.cur.Col >= n {
		if e.want == maxCol && e.vmode != 'V' {
			e.cur.Col = n
		} else if n > 0 {
			e.cur.Col = v.lastCol(e.cur.Line)
		} else {
			e.cur.Col = 0
		}
	}
}

func (e *Engine) execVisual(c cmd) {
	v := e.view()
	e.changeCursor = e.cur
	switch {
	case strings.HasPrefix(c.key, "<C-w>"):
		buf, cur := e.buffer, e.cur
		e.windowCmd(strings.TrimPrefix(c.key, "<C-w>"), c.count)
		if e.buffer != buf {
			e.endVisual()
		} else {
			// The window's cursor moves so the same Visual area stays selected.
			e.cur = cur
		}
	case e.scrollCmd(v, c.key, c.count):
	case c.obj != "":
		e.visualObject(v, c)
	case visualCmds[c.key] || operators[c.key] != "":
		e.visualCmd(v, c)
		if c.key == "y" || c.key == "Y" {
			e.rec = nil
		}
		if e.mode == Normal && e.visual == nil {
			e.endChange()
			e.setWant(e.view())
		}
	case c.key == "/" || c.key == "?":
		e.startCmdline(c.key[0], c, "")
	case motions[c.key] != nil:
		t, ok := motions[c.key](e, v, motionCtx{count: c.count, arg: c.arg, key: c.key})
		if !ok {
			return
		}
		if t.jump {
			e.setPcmark()
		}
		e.cur = t.pos
		switch {
		case t.keepWant || t.keepCol:
			e.cur.Col = v.colAt(e.cur.Line, e.want, e.want == maxCol && e.vmode == 'v')
		case t.wantEnd:
			e.want = maxCol
			if e.vmode != 'V' {
				e.cur.Col = v.lineLen(e.cur.Line)
			}
		}
		e.clampVisual(v)
		if !t.keepWant && !t.keepCol && !t.wantEnd {
			e.setWant(v)
		}
	}
	e.sync()
}

// visualRange is the operator range a charwise or linewise Visual area covers.
func (e *Engine) visualRange(v *view) oprange {
	start, end := *e.visual, e.cur
	if less(end, start) {
		start, end = end, start
	}
	if e.vmode == 'V' {
		return oprange{start: Pos{start.Line, 0}, end: Pos{end.Line, 0}, kind: linewise, curStart: start, visual: true}
	}
	r := oprange{start: start, end: end, kind: exclusive, curStart: start, visual: true}
	if end.Col >= v.lineLen(end.Line) {
		if end.Line+1 < v.lines() {
			r.end = Pos{end.Line + 1, 0}
		} else {
			r.end.Col = v.lineLen(end.Line)
		}
	} else {
		_, n := v.char(end)
		r.end.Col += n
	}
	return r
}

func (e *Engine) size(v *view) *visualSize {
	start, end := *e.visual, e.cur
	if less(end, start) {
		start, end = end, start
	}
	s := &visualSize{mode: e.vmode, lines: end.Line - start.Line + 1, wantEnd: e.want == maxCol}
	switch e.vmode {
	case 'v':
		if s.lines == 1 {
			s.cols = utf8.RuneCount(v.line(start.Line)[start.Col:min(end.Col+1, v.lineLen(end.Line))])
			if end.Col >= v.lineLen(end.Line) {
				s.cols++
			}
		} else {
			s.endCol = end.Col
		}
	case ctrlV:
		l, r := e.blockCols(v)
		s.cols = r - l
	}
	return s
}

// restoreArea makes a Visual area of the size an earlier Visual operator had, from the cursor, for ".".
func (e *Engine) restoreArea(v *view, s *visualSize) {
	start := e.cur
	e.visual = &start
	e.vmode = s.mode
	end := start
	end.Line = min(start.Line+s.lines-1, v.lines()-1)
	switch s.mode {
	case 'v':
		if s.lines == 1 {
			for i := 1; i < s.cols; i++ {
				_, n := v.char(end)
				if n == 0 {
					break
				}
				end.Col += n
			}
		} else {
			end.Col = s.endCol
		}
	case ctrlV:
		end.Col = v.colAt(end.Line, v.vcol(start.Line, start.Col)+s.cols-1, false)
	}
	e.cur = end
	if s.wantEnd {
		e.want = maxCol
	} else {
		e.setWant(v)
	}
	if s.mode != ctrlV {
		e.clampVisual(v)
	}
}

func (e *Engine) visualCmd(v *view, c cmd) {
	key := c.key
	switch key {
	case "<Esc>", "<C-c>":
		e.endVisual()
		e.mode = Normal
		e.clamp(v)
		return
	case "v", "V", "<C-v>":
		m := map[string]rune{"v": 'v', "V": 'V', "<C-v>": ctrlV}[key]
		if m == e.vmode {
			e.endVisual()
			e.clamp(v)
		} else {
			e.vmode = m
		}
		return
	case "o", "O":
		if key == "O" && e.vmode == ctrlV {
			a := *e.visual
			acol, ccol := a.Col, e.cur.Col
			e.visual = &Pos{a.Line, ccol}
			e.cur.Col = acol
			e.clampVisual(v)
			e.setWant(v)
			return
		}
		a := *e.visual
		e.visual = &Pos{e.cur.Line, e.cur.Col}
		e.cur = a
		e.setWant(v)
		return
	case "gv":
		e.reselect()
		return
	case ":":
		e.startCmdline(':', c, "'<,'>")
		return
	}
	// Everything else is an operator on the area.
	e.rec = &dotCmd{count: c.count, reg: c.reg, body: c.body, vis: e.size(v)}
	e.lastVisSize = e.rec.vis
	e.group = e.newGroup()
	block := e.vmode == ctrlV
	line := e.vmode == 'V'
	switch key {
	case "x", "<Del>":
		key = "d"
	case "s":
		key = "c"
	case "X", "D":
		if block && key == "D" {
			e.wantEndBlock()
			key = "d"
		} else {
			line = true
			key = "d"
		}
	case "Y":
		if !block {
			line = true
		}
		key = "y"
	case "C":
		if block {
			e.wantEndBlock()
		} else {
			line = true
		}
		key = "c"
	case "S", "R":
		line = true
		key = "c"
	case "~":
		key = "g~"
	case "u":
		key = "gu"
	case "U":
		key = "gU"
	}
	if fk := strings.TrimPrefix(key, "z"); fk != key && len(fk) == 1 && strings.Contains("dDoOcC", fk) {
		start, end := *e.visual, e.cur
		if less(end, start) {
			start, end = end, start
		}
		e.endVisual()
		e.rec = nil
		e.cur = start
		switch fk {
		case "d", "D":
			if e.foldManualAllowed(false) {
				e.deleteFolds(start.Line, end.Line, fk == "D")
			}
		default:
			e.foldRange(start.Line, end.Line, fk == "o" || fk == "O", fk == "O" || fk == "C")
		}
		e.clamp(e.view())
		return
	}
	if strings.HasSuffix(key, "<C-a>") || strings.HasSuffix(key, "<C-x>") {
		e.visualIncrement(v, c, strings.HasSuffix(key, "<C-x>"), key[0] == 'g')
		return
	}
	if line && !block {
		e.vmode = 'V'
	}
	if block {
		e.blockOp(v, key, c)
		return
	}
	r := e.visualRange(v)
	oldWant := e.want
	if line && e.vmode == 'V' {
		r.kind = linewise
		r.start.Col = 0
	}
	e.endVisual()
	e.cur = r.start
	switch key {
	case "r":
		e.visualReplace(v, r, c.arg)
	case "J", "gJ":
		n := max(r.end.Line-r.start.Line+1, 2)
		if r.kind != linewise && r.end.Col == 0 && r.end.Line > r.start.Line {
			n = max(r.end.Line-r.start.Line, 2)
		}
		e.join(v, n, key == "J")
	case "p", "P":
		e.visualPut(v, r, c, key == "P")
	case "I", "A":
		if key == "A" {
			e.cur = r.end
		}
		e.startInsert(v, c, "i")
	default:
		oc := c
		oc.op = key
		if key == ">" || key == "<" {
			r.shiftBy = c.count1()
		}
		e.applyOp(v, oc, r, oldWant)
		if key == "y" && r.kind == linewise {
			e.cur = Pos{r.start.Line, 0}
			if r.end.Line > r.start.Line {
				e.cur = r.curStart
			}
			e.clamp(e.view())
		}
	}
	if e.mode == Normal {
		e.clamp(e.view())
	}
}

func (e *Engine) visualReplace(v *view, r oprange, ch rune) {
	from, to := r.offsets(v)
	if r.kind == linewise {
		to = v.s.LineStart(r.end.Line) + v.lineLen(r.end.Line)
	}
	old := string(v.s.Slice(text.Range{Start: from, End: to}))
	var b strings.Builder
	for _, c := range old {
		switch {
		case c == '\n':
			b.WriteRune(c)
		case !composing(c):
			b.WriteRune(ch)
		}
	}
	e.do(e.group, text.Replace{Range: text.Range{Start: from, End: to}, Text: []byte(b.String())})
	e.cur = r.start
}

// visualPut replaces the area with a register. With P the replaced text isn't yanked.
func (e *Engine) visualPut(v *view, r oprange, c cmd, keep bool) {
	reg := c.reg
	if reg == 0 || reg == '"' {
		reg = e.unnamed
	}
	val, ok := e.ed.Register(reg)
	if !ok {
		return
	}
	from, to := r.offsets(v)
	removed := r.regText(v)
	body := strings.Repeat(val.Text, c.count1())
	ownLine := r.kind == linewise || val.Kind == edit.Linewise
	switch {
	case r.kind == linewise && val.Kind != edit.Linewise:
		body += "\n"
	case r.kind != linewise && val.Kind == edit.Linewise:
		body = "\n" + body
	}
	if r.kind == linewise && to == v.s.Len() && from > 0 && !strings.HasSuffix(string(v.s.Slice(text.Range{Start: from, End: to})), "\n") {
		body = strings.TrimSuffix(body, "\n")
	}
	e.do(e.group, text.Replace{Range: text.Range{Start: from, End: to}, Text: []byte(body)})
	nv := e.view()
	if !keep {
		e.deleteRegs(v, r, 0)
		if r.kind != linewise && r.lines() == 1 {
			e.setReg('-', removed)
		}
	}
	switch {
	case ownLine:
		line := nv.pos(from).Line
		if r.kind != linewise {
			line++
		}
		e.cur = Pos{line, nv.firstNonBlank(line)}
	case strings.Contains(body, "\n"):
		e.cur = nv.pos(from)
	default:
		_, n := utf8.DecodeLastRuneInString(body)
		e.cur = nv.pos(max(from, from+len(body)-n))
	}
	e.marks['['] = nv.pos(from)
	e.marks[']'] = nv.pos(max(from, lastRuneStart(nv, from+len(body))))
	e.lastVisual = &visualArea{start: nv.pos(from), end: nv.pos(max(from, lastRuneStart(nv, from+len(body)))), mode: e.lastVisualMode(r)}
	e.clamp(nv)
}

func (e *Engine) lastVisualMode(r oprange) rune {
	if r.kind == linewise {
		return 'V'
	}
	return 'v'
}
