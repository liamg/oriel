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

// visualObject applies a text object in Visual mode: on a one-character area it selects the object, on a
// bigger one it extends the area, as vim's current_word(), current_par() and current_block() do.
func (e *Engine) visualObject(v *view, c cmd) {
	single := *e.visual == e.cur
	kind := c.obj[1]
	inner := c.obj[0] == 'i'
	switch kind {
	case 's':
		e.visualSentence(v, c.count1(), !inner)
		return
	case '"', '\'', '`':
		e.visualQuote(v, c.count1(), !inner, kind)
		return
	}
	if !single {
		switch kind {
		case 'w', 'W':
			e.extendWord(v, c.count1(), !inner, kind == 'W')
			return
		case 'p':
			e.extendPar(v, c.count1(), !inner)
			return
		}
		if open, close, ok := blockPair(kind); ok {
			e.extendBlock(v, c.count1(), !inner, open, close)
			return
		}
	}
	s, ok := objects[c.obj](e, v, c.count1(), inner)
	if ok && s.empty && kind == 't' {
		// An empty tag block selects the tags themselves.
		s, ok = objects["at"](e, v, c.count1(), false)
	}
	if !ok || s.empty {
		return
	}
	end := s.end
	if s.kind == exclusive && !s.noAdjust {
		if end.Col > 0 {
			end.Col = v.prevCol(end.Line, end.Col)
		} else if end.Line > s.start.Line {
			end = Pos{end.Line - 1, v.lineLen(end.Line - 1)}
		}
	}
	if s.kind == linewise {
		e.vmode = 'V'
		p := s.start
		e.visual = &p
		e.cur = Pos{end.Line, 0}
		e.setWant(v)
		return
	}
	if e.vmode == 'V' {
		e.vmode = 'v'
	}
	p := s.start
	e.visual = &p
	e.cur = end
	e.setWant(v)
}

func blockPair(k byte) (rune, rune, bool) {
	switch k {
	case '(', ')', 'b':
		return '(', ')', true
	case '{', '}', 'B':
		return '{', '}', true
	case '[', ']':
		return '[', ']', true
	case '<', '>':
		return '<', '>', true
	}
	return 0, 0, false
}

// bckendWord is vim's bckend_word() with eol: back to the end of the previous word, stopping at a line start.
func bckendWord(v *view, p *Pos, big bool) bool {
	sclass := v.cls(*p, big)
	i := v.dec(p)
	if i == -1 {
		return false
	}
	if i == 1 {
		return true
	}
	if sclass != 0 {
		for v.cls(*p, big) == sclass {
			if i = v.dec(p); i == -1 || i == 1 {
				return true
			}
		}
	}
	for v.cls(*p, big) == 0 {
		if p.Col == 0 && v.lineLen(p.Line) == 0 {
			break
		}
		if i = v.dec(p); i == -1 || i == 1 {
			return true
		}
	}
	return true
}

func (e *Engine) extendWord(v *view, count int, include, big bool) {
	p := e.cur
	for ; count > 0; count-- {
		if less(p, *e.visual) {
			if v.decl(&p) == -1 {
				return
			}
			if include != (v.cls(p, big) != 0) {
				if !bckWord(v, &p, 1, big, true) {
					return
				}
			} else {
				if !bckendWord(v, &p, big) {
					return
				}
				v.incl(&p)
			}
			continue
		}
		if v.incl(&p) == -1 {
			return
		}
		if include != (v.cls(p, big) == 0) {
			if !fwdWord(v, &p, 1, big, true) && count > 1 {
				return
			}
			v.oneleft(&p)
		} else if !endWord(v, &p, 1, big, true, true) {
			return
		}
	}
	if e.vmode == 'V' {
		e.vmode = 'v'
	}
	e.cur = p
	e.setWant(v)
}

func (e *Engine) extendPar(v *view, count int, include bool) {
	line := e.cur.Line
	dir := 1
	last := v.lines() - 1
	if line < e.visual.Line {
		dir = -1
	}
	edge := last
	if dir < 0 {
		edge = 0
	}
	for ; count > 0; count-- {
		if line == edge {
			break
		}
		prevWhite := -1
		for t := 0; t < 2; t++ {
			line += dir
			white := 0
			if v.lineWhite(line) {
				white = 1
			}
			if prevWhite == white {
				line -= dir
				break
			}
			for line != edge {
				next := line + dir
				nw := v.lineWhite(next)
				ps := false
				if white == 0 {
					l := line
					if dir > 0 {
						l = line + 1
					}
					ps = v.startPS(l, 0)
				}
				if nw != (white == 1) || ps {
					break
				}
				line += dir
			}
			if !include || line == edge {
				break
			}
			prevWhite = white
		}
	}
	e.vmode = 'V'
	e.cur = Pos{line, 0}
	e.setWant(v)
}

func (e *Engine) extendBlock(v *view, count int, include bool, open, close rune) {
	oldStart, oldEnd := *e.visual, e.cur
	cur := e.cur
	if less(*e.visual, e.cur) {
		cur = *e.visual
	} else {
		oldStart, oldEnd = e.cur, *e.visual
	}
	var start Pos
	found := false
	p := cur
	for ; count > 0; count-- {
		q, ok := findUnmatched(v, p, open, close, false)
		if !ok {
			return
		}
		p, start, found = q, q, true
	}
	if !found {
		return
	}
	end, ok := findUnmatched(v, start, open, close, true)
	if !ok {
		return
	}
	sol := false
	for !include {
		s, en := start, end
		v.incl(&s)
		sol = en.Col == 0
		v.decl(&en)
		for v.inindent(en, 1) {
			sol = true
			if v.decl(&en) != 0 {
				break
			}
		}
		if !less(s, oldStart) && !less(oldEnd, en) && s != en {
			q := oldStart
			v.decl(&q)
			ns, ok := findUnmatched(v, q, open, close, false)
			if !ok {
				return
			}
			ne, ok := findUnmatched(v, ns, open, close, true)
			if !ok {
				return
			}
			start, end, oldStart = ns, ne, ns
			continue
		}
		start, end = s, en
		break
	}
	if sol && v.gchar(end) != 0 {
		v.inc(&end)
	}
	e.visual = &start
	e.vmode = 'v'
	e.cur = end
	e.setWant(v)
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

func (e *Engine) wantEndBlock() { e.want = maxCol }

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

// blockCols are the screen columns [left, right) a block covers.
func (e *Engine) blockCols(v *view) (int, int) {
	a, c := *e.visual, e.cur
	va, vc := v.vcol(a.Line, a.Col), v.vcol(c.Line, c.Col)
	width := func(p Pos, vc int) int {
		r, n := v.char(p)
		if n == 0 {
			return 1
		}
		return max(charWidth(r, vc), 1)
	}
	ea, ec := va+width(a, va), vc+width(c, vc)
	return min(va, vc), max(ea, ec)
}

// lineSpan is the byte range of a line under screen columns [left, right), widened to whole characters.
func lineSpan(v *view, line, left, right int) (int, int, bool) {
	l := v.line(line)
	vc, i := 0, 0
	start, end := -1, -1
	for i < len(l) {
		r, n := utf8.DecodeRune(l[i:])
		w := charWidth(r, vc)
		if start < 0 && vc+w > left {
			start = i
		}
		if vc >= right {
			end = i
			break
		}
		vc += w
		i += n
	}
	if start < 0 {
		return len(l), len(l), false
	}
	if end < 0 {
		end = len(l)
	}
	return start, end, true
}

// blockOp runs an operator over a Visual block.
func (e *Engine) blockOp(v *view, op string, c cmd) {
	a, cur := *e.visual, e.cur
	top, bot := min(a.Line, cur.Line), max(a.Line, cur.Line)
	left, right := e.blockCols(v)
	toEnd := e.want == maxCol
	e.endVisual()
	type span struct{ line, from, to int }
	var spans []span
	for l := top; l <= bot; l++ {
		r := right
		if toEnd {
			r = maxCol
		}
		f, t, _ := lineSpan(v, l, left, r)
		spans = append(spans, span{l, f, t})
	}
	startPos := Pos{top, spans[0].from}
	text_ := func(sp span) string { return string(v.line(sp.line)[sp.from:sp.to]) }
	switch op {
	case "y", "d":
		var parts []string
		for _, sp := range spans {
			parts = append(parts, text_(sp))
		}
		val := edit.Register{Text: strings.Join(parts, "\n"), Kind: edit.Blockwise}
		reg := c.reg
		if op == "y" {
			if reg == 0 || reg == '"' {
				reg = '0'
			}
			if reg != '_' {
				e.setReg(reg, val)
			}
		} else if reg != '_' {
			if reg != 0 && reg != '"' {
				e.setReg(reg, val)
			}
			if len(spans) > 1 || reg == 0 || reg == '"' {
				for n := '9'; n > '1'; n-- {
					if prev, ok := e.ed.Register(n - 1); ok {
						e.ed.SetRegister(n, prev)
					}
				}
				e.ed.SetRegister('1', val)
				e.unnamed = '1'
			}
		}
		if op == "d" {
			var rs []text.Replace
			for _, sp := range spans {
				st := v.s.LineStart(sp.line)
				if sp.to > sp.from {
					rs = append(rs, text.Replace{Range: text.Range{Start: st + sp.from, End: st + sp.to}})
				}
			}
			e.cur = startPos
			if len(rs) > 0 {
				e.do(e.group, rs...)
			}
		}
		e.cur = startPos
		e.clamp(e.view())
		e.setWant(e.view())
	case "c":
		var parts []string
		for _, sp := range spans {
			parts = append(parts, text_(sp))
		}
		val := edit.Register{Text: strings.Join(parts, "\n"), Kind: edit.Blockwise}
		if c.reg != '_' {
			if c.reg != 0 && c.reg != '"' {
				e.setReg(c.reg, val)
			}
			e.ed.SetRegister('1', val)
			e.unnamed = '1'
		}
		var rs []text.Replace
		for _, sp := range spans {
			st := v.s.LineStart(sp.line)
			if sp.to > sp.from {
				rs = append(rs, text.Replace{Range: text.Range{Start: st + sp.from, End: st + sp.to}})
			}
		}
		e.cur = startPos
		if len(rs) > 0 {
			e.do(e.group, rs...)
		}
		e.blockInsert(top, bot, left, false, c)
	case "I":
		e.cur = startPos
		e.blockInsert(top, bot, left, false, c)
	case "A":
		col := right
		if toEnd {
			col = maxCol
		}
		e.blockInsert(top, bot, col, true, c)
		e.ins.block.left = startPos
	case "r":
		var rs []text.Replace
		for _, sp := range spans {
			if sp.from >= sp.to {
				continue
			}
			st := v.s.LineStart(sp.line)
			vf, vt := v.vcol(sp.line, sp.from), v.vcol(sp.line, sp.to)
			r := right
			if toEnd {
				r = vt
			}
			body := strings.Repeat(" ", max(0, left-vf)) + strings.Repeat(string(c.arg), min(vt, r)-max(vf, left)) + strings.Repeat(" ", max(0, vt-r))
			rs = append(rs, text.Replace{Range: text.Range{Start: st + sp.from, End: st + sp.to}, Text: []byte(body)})
		}
		e.cur = startPos
		if len(rs) > 0 {
			e.do(e.group, rs...)
		}
		e.cur = startPos
	case "g~", "gu", "gU", "g?":
		f := caseFunc(op)
		var rs []text.Replace
		for _, sp := range spans {
			st := v.s.LineStart(sp.line)
			rs = append(rs, text.Replace{Range: text.Range{Start: st + sp.from, End: st + sp.to}, Text: []byte(strings.Map(f, text_(sp)))})
		}
		e.cur = startPos
		e.do(e.group, rs...)
		e.cur = startPos
	case ">", "<":
		var rs []text.Replace
		amount := e.opts.ShiftWidth * c.count1()
		for _, sp := range spans {
			l := v.line(sp.line)
			if sp.from >= len(l) {
				continue
			}
			st := v.s.LineStart(sp.line)
			if op == ">" {
				ws := sp.from
				for ws < len(l) && isWhite(rune(l[ws])) {
					ws++
				}
				startV := v.vcol(sp.line, sp.from)
				total := v.vcol(sp.line, ws) - startV + amount
				rs = append(rs, text.Replace{Range: text.Range{Start: st + sp.from, End: st + ws}, Text: []byte(e.makeIndentFrom(startV, total))})
				continue
			}
			if r, ok := e.shiftBlockLeft(v, sp.line, left, amount); ok {
				rs = append(rs, text.Replace{Range: text.Range{Start: st, End: st + len(l)}, Text: []byte(r)})
			}
		}
		e.cur = startPos
		if len(rs) > 0 {
			e.do(e.group, rs...)
		}
		e.cur = startPos
	case "J", "gJ":
		e.cur = Pos{top, e.cur.Col}
		e.join(v, max(bot-top+1, 2), op == "J")
	case "p", "P":
		e.blockPut(v, spans[0].from, top, bot, left, right, c, op == "P")
	}
	if e.mode == Normal {
		e.clamp(e.view())
	}
}

// shiftBlockLeft is vim's shift_block() to the left: the white space at the block's start shrinks by up to
// amount columns, and the text before it is kept, with a tab it cuts into turned into spaces.
func (e *Engine) shiftBlockLeft(v *view, line, left, amount int) (string, bool) {
	l := v.line(line)
	f, _, _ := lineSpan(v, line, left, left+1)
	if f >= len(l) {
		return "", false
	}
	nonWhite := f
	nonWhiteCol := v.vcol(line, f)
	if nonWhiteCol < left {
		// A character straddles the block's start: start after it.
		_, n := v.char(Pos{line, f})
		nonWhite += n
		nonWhiteCol = v.vcol(line, nonWhite)
	}
	for nonWhite < len(l) && isWhite(rune(l[nonWhite])) {
		nonWhite++
	}
	nonWhiteCol = v.vcol(line, nonWhite)
	shift := min(nonWhiteCol-left, amount)
	if shift <= 0 {
		return "", false
	}
	dest := nonWhiteCol - shift
	end := 0
	for end < len(l) {
		_, n := v.char(Pos{line, end})
		if v.vcol(line, end+n) > dest {
			break
		}
		end += n
	}
	fill := dest - v.vcol(line, end)
	return string(l[:end]) + strings.Repeat(" ", fill) + string(l[nonWhite:]), true
}

// makeIndentFrom builds white space covering width screen columns starting at column start: tabs and
// spaces, or only spaces with 'expandtab'.
func (e *Engine) makeIndentFrom(start, width int) string {
	if e.opts.ExpandTab {
		return strings.Repeat(" ", width)
	}
	var b strings.Builder
	col := start
	end := start + width
	for (col/tabstop+1)*tabstop <= end {
		b.WriteByte('\t')
		col = (col/tabstop + 1) * tabstop
	}
	b.WriteString(strings.Repeat(" ", end-col))
	return b.String()
}

// blockInsert starts insert mode for a block I, A or c: what is typed on the first line goes on every line at Esc.
func (e *Engine) blockInsert(top, bot, vcol int, appendMode bool, c cmd) {
	v := e.view()
	if appendMode {
		for l := top; l <= bot; l++ {
			n := v.vcol(l, v.lineLen(l))
			if vcol != maxCol && n < vcol {
				st := v.s.LineStart(l) + v.lineLen(l)
				e.do(e.group, text.Replace{Range: text.Range{Start: st, End: st}, Text: []byte(strings.Repeat(" ", vcol-n))})
				v = e.view()
			}
		}
		if vcol == maxCol {
			e.cur = Pos{top, v.lineLen(top)}
		} else {
			f, _, _ := lineSpan(v, top, vcol, vcol+1)
			e.cur = Pos{top, f}
		}
	} else {
		f, _, _ := lineSpan(v, top, vcol, vcol+1)
		e.cur = Pos{top, f}
	}
	e.startInsert(v, cmd{count: 1}, "i")
	e.ins.block = &blockInsert{top: top, bot: bot, vcol: vcol, appendMode: appendMode, start: e.cur, startLen: v.lineLen(top), startText: string(v.line(top)), change: c.key == "c" || c.key == "C" || c.key == "s", left: e.cur}
}

// blockInsert is a pending block insert, applied to the other lines when insert mode ends.
type blockInsert struct {
	top, bot, vcol int
	appendMode     bool
	start          Pos
	startLen       int
	startText      string
	change         bool // c and C leave the cursor after the text, I and A at the block's start
	left           Pos
}

// finishBlock copies the text typed on the block's first line to its other lines.
func (e *Engine) finishBlock(b *blockInsert) {
	v := e.view()
	if e.cur.Line != b.top || v.lineLen(b.top) < b.startLen {
		return
	}
	// What was typed is what the first line gained, wherever the cursor keys put it.
	now := string(v.line(b.top))
	pre := 0
	for pre < len(b.startText) && pre < len(now) && b.startText[pre] == now[pre] {
		pre++
	}
	pre = min(pre, b.start.Col+len(now)-len(b.startText))
	if pre < b.start.Col {
		pre = b.start.Col
	}
	typed := now[pre : pre+len(now)-len(b.startText)]
	if typed == "" || strings.Contains(typed, "\n") {
		return
	}
	if pre != b.start.Col && b.vcol != maxCol {
		b.vcol = v.vcol(b.top, pre)
		b.left = Pos{b.top, pre}
	}
	var rs []text.Replace
	for l := b.top + 1; l <= b.bot; l++ {
		var col int
		if b.vcol == maxCol {
			col = v.lineLen(l)
		} else {
			if !b.appendMode && v.vcol(l, v.lineLen(l)) < b.vcol {
				continue
			}
			f, _, _ := lineSpan(v, l, b.vcol, b.vcol+1)
			col = f
			if vf := v.vcol(l, f); vf < b.vcol && f < v.lineLen(l) {
				st := v.s.LineStart(l) + f
				if v.gchar(Pos{l, f}) == '\t' {
					// The column falls inside a tab: split it into spaces around the text.
					ve := v.vcol(l, f+1)
					body := strings.Repeat(" ", b.vcol-vf) + typed + strings.Repeat(" ", ve-b.vcol)
					rs = append(rs, text.Replace{Range: text.Range{Start: st, End: st + 1}, Text: []byte(body)})
				} else {
					// Inside a wider character: pad up to the column and put the text before it.
					rs = append(rs, text.Replace{Range: text.Range{Start: st, End: st}, Text: []byte(strings.Repeat(" ", b.vcol-vf) + typed)})
				}
				continue
			}
		}
		st := v.s.LineStart(l) + col
		rs = append(rs, text.Replace{Range: text.Range{Start: st, End: st}, Text: []byte(typed)})
	}
	if len(rs) > 0 {
		e.do(e.group, rs...)
	}
}

// blockPut replaces a block with a register: a block register goes in line by line, anything else replaces each line's part.
func (e *Engine) blockPut(v *view, firstCol, top, bot, left, right int, c cmd, keep bool) {
	reg := c.reg
	if reg == 0 || reg == '"' {
		reg = e.unnamed
	}
	val, ok := e.ed.Register(reg)
	if !ok {
		return
	}
	var parts []string
	var rs []text.Replace
	for l := top; l <= bot; l++ {
		f, t, _ := lineSpan(v, l, left, right)
		parts = append(parts, string(v.line(l)[f:t]))
		st := v.s.LineStart(l)
		rs = append(rs, text.Replace{Range: text.Range{Start: st + f, End: st + t}})
	}
	if val.Kind == edit.Linewise {
		e.cur = Pos{top, firstCol}
		e.do(e.group, rs...)
		nv := e.view()
		e.insertLines(nv, bot, strings.Repeat(val.Text, c.count1()))
		nv = e.view()
		e.cur = Pos{bot + 1, nv.firstNonBlank(bot + 1)}
		if !keep {
			e.setReg('"', edit.Register{Text: strings.Join(parts, "\n"), Kind: edit.Blockwise})
			e.ed.SetRegister('1', edit.Register{Text: strings.Join(parts, "\n"), Kind: edit.Blockwise})
			e.unnamed = '1'
		}
		return
	}
	if val.Kind == edit.Blockwise || !strings.Contains(val.Text, "\n") {
		lines := strings.Split(val.Text, "\n")
		for i := range rs {
			if val.Kind == edit.Blockwise {
				if i < len(lines) {
					rs[i].Text = []byte(lines[i])
				}
			} else {
				rs[i].Text = []byte(strings.Repeat(val.Text, c.count1()))
			}
		}
	}
	e.cur = Pos{top, firstCol}
	e.do(e.group, rs...)
	if !keep {
		e.setReg('"', edit.Register{Text: strings.Join(parts, "\n"), Kind: edit.Blockwise})
		e.ed.SetRegister('1', edit.Register{Text: strings.Join(parts, "\n"), Kind: edit.Blockwise})
		e.unnamed = '1'
	}
	nv := e.view()
	e.cur = Pos{top, firstCol}
	if !strings.Contains(val.Text, "\n") && val.Kind != edit.Blockwise {
		if n := len(val.Text) * c.count1(); n > 0 {
			_, sz := utf8.DecodeLastRuneInString(val.Text)
			e.cur.Col = firstCol + n - sz
		}
	}
	e.clamp(nv)
}

// putBlock puts a blockwise register at the cursor's screen column, one line of the block per buffer line.
func (e *Engine) putBlock(v *view, val edit.Register, after, gp bool, count int) {
	lines := strings.Split(val.Text, "\n")
	width := 0
	for _, l := range lines {
		width = max(width, utf8.RuneCountInString(l))
	}
	col := v.vcol(e.cur.Line, e.cur.Col)
	if after && v.lineLen(e.cur.Line) > 0 {
		r, _ := v.char(e.cur)
		col += charWidth(r, col)
	}
	var rs []text.Replace
	var tail strings.Builder
	for i, l := range lines {
		piece := strings.Repeat(l+strings.Repeat(" ", width-utf8.RuneCountInString(l)), count)
		line := e.cur.Line + i
		if line >= v.lines() {
			tail.WriteString("\n" + strings.Repeat(" ", col) + strings.TrimRight(piece, " "))
			continue
		}
		n := v.vcol(line, v.lineLen(line))
		st := v.s.LineStart(line)
		if n < col {
			rs = append(rs, text.Replace{Range: text.Range{Start: st + v.lineLen(line), End: st + v.lineLen(line)}, Text: []byte(strings.Repeat(" ", col-n) + strings.TrimRight(piece, " "))})
			continue
		}
		f, _, _ := lineSpan(v, line, col, col+1)
		if f >= v.lineLen(line) {
			piece = strings.TrimRight(piece, " ")
		}
		rs = append(rs, text.Replace{Range: text.Range{Start: st + f, End: st + f}, Text: []byte(piece)})
	}
	if tail.Len() > 0 {
		rs = append(rs, text.Replace{Range: text.Range{Start: v.s.Len(), End: v.s.Len()}, Text: []byte(tail.String())})
	}
	e.do(e.group, rs...)
	nv := e.view()
	f, _, _ := lineSpan(nv, e.cur.Line, col, col+1)
	e.cur.Col = f
	if gp {
		e.cur = Pos{min(e.cur.Line+len(lines)-1, nv.lines()-1), 0}
		g, _, _ := lineSpan(nv, e.cur.Line, col+width*count, col+width*count+1)
		e.cur.Col = g
	}
	e.clamp(nv)
	e.setWant(nv)
}

// visualSentence is vim's current_sent() with a Visual area.
func (e *Engine) visualSentence(v *view, count int, include bool) {
	startPos := e.cur
	pos := startPos
	cur, _ := findSent(v, e.cur, 1, 1)
	extend := *e.visual != startPos
	for {
		if extend {
			if less(startPos, *e.visual) {
				atStart := true
				v.decl(&pos)
				for less(pos, cur) {
					if !isWhite(v.gchar(pos)) {
						atStart = false
						break
					}
					v.incl(&pos)
				}
				if !atStart {
					cur, _ = findSent(v, cur, -1, 1)
					if cur == startPos {
						atStart = true
					} else {
						cur, _ = findSent(v, cur, 1, 1)
					}
				}
				n := count
				if include {
					n *= 2
				}
				for ; n > 0; n-- {
					if atStart {
						findFirstBlank(v, &cur)
					}
					if !atStart || !include && !isWhite(v.gchar(cur)) {
						cur, _ = findSent(v, cur, -1, 1)
					}
					atStart = !atStart
				}
			} else {
				v.incl(&pos)
				atStart := true
				if pos != cur {
					atStart = false
					for less(pos, cur) {
						if !isWhite(v.gchar(pos)) {
							atStart = true
							break
						}
						v.incl(&pos)
					}
					if atStart {
						cur, _ = findSent(v, cur, -1, 1)
					} else {
						cur = startPos
					}
				}
				n := count
				if include {
					n *= 2
				}
				cur = findSentForward(v, cur, n, atStart)
			}
			e.cur = cur
			e.vmode = 'v'
			e.setWant(v)
			return
		}
		// One character selected: select whole sentences, as for an operator.
		p := startPos
		for isWhite(v.gchar(p)) {
			v.incl(&p)
		}
		var start Pos
		startBlank := false
		if p == cur {
			startBlank = true
			start = startPos
			findFirstBlank(v, &start)
		} else {
			cur, _ = findSent(v, cur, -1, 1)
			start = cur
		}
		ncount := count
		if include {
			ncount = count * 2
		} else if startBlank {
			ncount--
		}
		if ncount > 0 {
			cur = findSentForward(v, cur, ncount, true)
		} else {
			v.decl(&cur)
		}
		if include {
			if startBlank {
				findFirstBlank(v, &cur)
				if isWhite(v.gchar(cur)) {
					v.decl(&cur)
				}
			} else if !isWhite(v.gchar(cur)) {
				findFirstBlank(v, &start)
			}
		}
		if start == cur {
			extend = true
			pos = startPos
			continue
		}
		e.visual = &start
		e.cur = cur
		e.vmode = 'v'
		e.setWant(v)
		return
	}
}

// visualQuote is vim's current_quote() with a Visual area on one line.
func (e *Engine) visualQuote(v *view, count int, include bool, q byte) {
	if e.visual.Line != e.cur.Line {
		return
	}
	line := v.line(e.cur.Line)
	at := func(i int) byte {
		if i >= 0 && i < len(line) {
			return line[i]
		}
		return 0
	}
	vis := *e.visual
	befCurs := less(vis, e.cur)
	empty := vis == e.cur
	insideQuotes, selectedQuote := false, false
	colStart, colEnd := e.cur.Col, 0
	if !empty {
		var i int
		if befCurs {
			insideQuotes = vis.Col > 0 && at(vis.Col-1) == q && at(e.cur.Col) != 0 && at(e.cur.Col+1) == q
			i, colEnd = vis.Col, e.cur.Col
		} else {
			insideQuotes = e.cur.Col > 0 && at(e.cur.Col-1) == q && at(vis.Col) != 0 && at(vis.Col+1) == q
			i, colEnd = e.cur.Col, vis.Col
		}
		for ; i <= colEnd && at(i) != 0; i++ {
			if at(i) == q {
				selectedQuote = true
				break
			}
		}
	}
	switch {
	case !empty && at(colStart) == q:
		if befCurs {
			colStart = findNextQuote(line, colStart+1, q, false)
			if colStart < 0 {
				return
			}
			colEnd = findNextQuote(line, colStart+1, q, true)
			if colEnd < 0 {
				colEnd = colStart
				colStart = e.cur.Col
			}
		} else {
			colEnd = findPrevQuote(line, colStart, q)
			if at(colEnd) != q {
				return
			}
			colStart = findPrevQuote(line, colEnd, q)
			if at(colStart) != q {
				colStart = colEnd
				colEnd = e.cur.Col
			}
		}
	case at(colStart) == q || !empty:
		first := colStart
		if !empty {
			if befCurs {
				first = findNextQuote(line, colStart, q, false)
			} else {
				first = findPrevQuote(line, colStart, q)
			}
		}
		colStart = 0
		for {
			colStart = findNextQuote(line, colStart, q, false)
			if colStart < 0 || colStart > first {
				return
			}
			colEnd = findNextQuote(line, colStart+1, q, true)
			if colEnd < 0 {
				return
			}
			if colStart <= first && first <= colEnd {
				break
			}
			colStart = colEnd + 1
		}
	default:
		colStart = findPrevQuote(line, colStart, q)
		if at(colStart) != q {
			colStart = findNextQuote(line, colStart, q, false)
			if colStart < 0 {
				return
			}
		}
		colEnd = findNextQuote(line, colStart+1, q, true)
		if colEnd < 0 {
			return
		}
	}
	if include {
		if isWhite(rune(at(colEnd + 1))) {
			for isWhite(rune(at(colEnd + 1))) {
				colEnd++
			}
		} else {
			for colStart > 0 && isWhite(rune(at(colStart-1))) {
				colStart--
			}
		}
	}
	if !include && count < 2 && (empty || !insideQuotes) {
		colStart++
	}
	l := e.cur.Line
	if empty || befCurs && !selectedQuote && (insideQuotes || at(vis.Col) != q && (vis.Col == 0 || at(vis.Col-1) != q)) {
		e.visual = &Pos{l, colStart}
	}
	cur := Pos{l, colEnd}
	if include || count > 1 || !empty && insideQuotes {
		v.inc(&cur)
	}
	if empty || befCurs {
		v.dec(&cur)
		e.cur = cur
	} else {
		if insideQuotes || !selectedQuote && at(vis.Col) != q && (at(vis.Col) == 0 || at(vis.Col+1) != q) {
			v.dec(&cur)
			e.visual = &cur
		}
		e.cur = Pos{l, colStart}
	}
	e.vmode = 'v'
	e.setWant(v)
}
