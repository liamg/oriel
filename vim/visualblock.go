package vim

import (
	"strings"
	"unicode/utf8"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/text"
)

func (e *Engine) wantEndBlock() { e.want = maxCol }

// blockCols are the screen columns [left, right) a block covers.
func (e *Engine) blockCols(v *view) (int, int) {
	a, c := *e.visual, e.cur
	va, vc := v.vcol(a.Line, a.Col), v.vcol(c.Line, c.Col)
	width := func(p Pos, vc int) int {
		r, n := v.char(p)
		if n == 0 {
			return 1
		}
		return max(charWidth(r, vc, v.ts), 1)
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
		w := charWidth(r, vc, v.ts)
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
	case "y", "d", "c":
		var parts []string
		for _, sp := range spans {
			parts = append(parts, text_(sp))
		}
		val := edit.Register{Text: strings.Join(parts, "\n"), Kind: edit.Blockwise}
		if op == "y" {
			reg := c.reg
			if reg == 0 || reg == '"' {
				reg = '0'
			}
			if reg != '_' {
				e.setReg(reg, val)
			}
			e.cur = startPos
			e.clamp(e.view())
			e.setWant(e.view())
			return
		}
		e.storeDeleted(val, c.reg, len(spans) > 1, len(spans) == 1)
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
		if op == "c" {
			e.blockInsert(top, bot, left, false, c)
			return
		}
		e.clamp(e.view())
		e.setWant(e.view())
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
		amount := e.opts.shiftWidth() * c.count1()
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
	ts := e.opts.TabStop
	for (col/ts+1)*ts <= end {
		b.WriteByte('\t')
		col = (col/ts + 1) * ts
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
			e.storeDeleted(edit.Register{Text: strings.Join(parts, "\n"), Kind: edit.Blockwise}, 0, len(parts) > 1, len(parts) == 1)
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
		e.storeDeleted(edit.Register{Text: strings.Join(parts, "\n"), Kind: edit.Blockwise}, 0, len(parts) > 1, len(parts) == 1)
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
		col += charWidth(r, col, v.ts)
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
