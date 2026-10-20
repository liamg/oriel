package vim

// Wrapping (options.txt *'wrap'*): a line longer than the window takes as many screen rows as it needs. The window's
// top stays a whole line, as without 'smoothscroll', and gj, gk, g0, g^, gm and g$ move by screen rows.

// rowsOf is how many screen rows line takes: one for a closed fold or with 'nowrap', else its width over the window's
// columns.
func (e *Engine) rowsOf(v *view, line int) int { return e.rowsIn(e.window, v, line) }

func (e *Engine) rowsIn(w *window, v *view, line int) int {
	if !e.opts.Wrap || line < 0 || line >= v.lines() {
		return 1
	}
	if _, _, ok := w.fold.closedAt(line); ok {
		return 1
	}
	cols := e.widthOf(w)
	return max((v.vcol(line, v.lineLen(line))+cols-1)/cols, 1)
}

// Wrap says the pane's long lines wrap onto more screen rows ('wrap').
func (p Pane) Wrap() bool { return p.e.opts.Wrap }

// Cols is how many screen columns the pane has for text.
func (p Pane) Cols() int { return p.e.widthOf(p.w) }

// RowsOf is how many screen rows line takes in the pane: more than one for a long line with 'wrap'.
func (p Pane) RowsOf(line int) int { return p.e.rowsIn(p.w, p.view(), line) }

// rowCols are the screen columns of window row r of a wrapped line: the first and one past the last.
func (e *Engine) rowCols(r int) (int, int) {
	cols := e.widthOf(e.window)
	return r * cols, (r + 1) * cols
}

// screenStep is gj and gk, as nvim's nv_screengo: down or up n screen rows, keeping the column within the row; with
// 'nowrap' it moves as j and k do.
func screenStep(e *Engine, v *view, n int) (target, bool) {
	if !e.opts.Wrap {
		t, ok := vertical(e, v, n)
		t.kind = exclusive
		return t, ok
	}
	cols := e.widthOf(e.window)
	line, want := e.cur.Line, e.want
	if want == maxCol {
		want = max(v.vcol(line, v.lineLen(line))-1, 0)
	}
	for ; n != 0; n -= sign(n > 0) {
		width := v.vcol(line, v.lineLen(line))
		switch {
		case n > 0 && want+cols < width && e.rowsOf(v, line) > 1:
			want += cols
		case n > 0:
			next, ok := foldStep(v, line, 1)
			if !ok || next == line {
				return target{}, false
			}
			line, want = next, want%cols
		case want >= cols:
			want -= cols
		default:
			if line == 0 {
				return target{}, false
			}
			prev, _ := foldStep(v, line, -1)
			line = prev
			if rows := e.rowsOf(v, line); rows > 1 {
				want += (rows - 1) * cols
			}
		}
	}
	e.want = want
	return target{pos: Pos{Line: line, Col: v.colAt(line, want, false)}, kind: exclusive, keepWant: true}, true
}

func screenDown(e *Engine, v *view, m motionCtx) (target, bool) { return screenStep(e, v, m.count1()) }
func screenUp(e *Engine, v *view, m motionCtx) (target, bool)   { return screenStep(e, v, -m.count1()) }

// screenRow is the cursor's screen row: its first and one-past-last columns, wrapped or scrolled sideways.
func (e *Engine) screenRow(v *view) (int, int) {
	vc := v.vcol(e.cur.Line, e.cur.Col)
	if e.opts.Wrap {
		return e.rowCols(vc / e.widthOf(e.window))
	}
	return e.window.left, e.window.left + e.widthOf(e.window)
}

// screenStart is g0: the first character of the screen row.
func screenStart(e *Engine, v *view, m motionCtx) (target, bool) {
	from, _ := e.screenRow(v)
	return target{pos: Pos{e.cur.Line, v.colAt(e.cur.Line, from, false)}}, true
}

// screenFirstNonBlank is g^: the first non-blank character of the screen row.
func screenFirstNonBlank(e *Engine, v *view, m motionCtx) (target, bool) {
	from, to := e.screenRow(v)
	l := e.cur.Line
	col := v.colAt(l, from, false)
	for col < v.lineLen(l) && v.vcol(l, col) < to-1 && isWhite(rune(v.line(l)[col])) {
		_, n := v.char(Pos{l, col})
		col += n
	}
	return target{pos: Pos{l, col}}, true
}

// screenMiddle is gm: half the screen row's width along it.
func screenMiddle(e *Engine, v *view, m motionCtx) (target, bool) {
	from, to := e.screenRow(v)
	return target{pos: Pos{e.cur.Line, v.colAt(e.cur.Line, from+(to-from)/2, false)}}, true
}

// screenEnd is g$: the last character of the screen row.
func screenEnd(e *Engine, v *view, m motionCtx) (target, bool) {
	_, to := e.screenRow(v)
	return target{pos: Pos{e.cur.Line, v.colAt(e.cur.Line, to-1, false)}, kind: inclusive}, true
}

func init() {
	for k, m := range map[string]motion{
		"gj": screenDown, "g<Down>": screenDown, "gk": screenUp, "g<Up>": screenUp,
		"g0": screenStart, "g<Home>": screenStart, "g^": screenFirstNonBlank, "gm": screenMiddle, "g$": screenEnd, "g<End>": screenEnd,
	} {
		motions[k] = m
	}
}
