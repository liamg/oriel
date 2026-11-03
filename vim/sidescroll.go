package vim

// Scrolling horizontally (scroll.txt *scroll-horizontal*): with 'nowrap', a window shows its lines from its left
// column, kept on the cursor as 'sidescroll' and 'sidescrolloff' say, and moved by zl, zh, zL, zH, zs and ze.

// Left is the current window's first screen column shown.
func (e *Engine) Left() int { return e.window.left }

// Left is the window's first screen column shown.
func (p Pane) Left() int { return p.w.left }

// SetCols tells window id how many screen columns it has for text.
func (e *Engine) SetCols(id WindowID, cols int) {
	w := e.windowByID(id)
	if w == nil || w.width == cols {
		return
	}
	w.width = cols
	if w == e.window {
		e.updateLeft(e.view())
	}
}

// ScrollLeft shows window id from screen column col, as the wheel does sideways; the cursor follows the screen.
func (e *Engine) ScrollLeft(id WindowID, col int) {
	if w := e.windowByID(id); w != nil && !e.opts.Wrap {
		prev := e.window
		e.window, e.buffer = w, w.buf
		w.left = max(col, 0)
		e.leftChanged(e.view())
		e.sync()
		e.window, e.buffer = prev, prev.buf
	}
}

func (e *Engine) widthOf(w *window) int {
	if w.width > 0 {
		return w.width
	}
	if l, ok := e.layout.(interface{ Cols(WindowID) int }); ok {
		return max(l.Cols(WindowID(w.id)), 1)
	}
	return screenWidth
}

// cursorCols are the screen columns the character under the cursor covers.
func (e *Engine) cursorCols(v *view) (int, int) {
	start := v.vcol(e.cur.Line, e.cur.Col)
	end := start
	if _, n := v.char(e.cur); n > 0 {
		end = v.vcol(e.cur.Line, e.cur.Col+n) - 1
	}
	return start, max(end, start)
}

// updateLeft keeps the cursor's column on screen, as nvim's curs_columns: scrolled by at least 'sidescroll', or with
// the cursor put in the middle when it's far off or 'sidescroll' is 0.
func (e *Engine) updateLeft(v *view) {
	w := e.window
	if e.opts.Wrap {
		w.left = 0
		return
	}
	width := e.widthOf(w)
	siso := min(e.opts.SideScrollOff, (width-1)/2)
	start, end := e.cursorCols(v)
	offLeft := start - w.left - siso
	offRight := end - (w.left + width - siso) + 1
	if offLeft >= 0 && offRight <= 0 {
		return
	}
	diff := offRight
	if offLeft < 0 {
		diff = -offLeft
	}
	var left int
	if e.opts.SideScroll == 0 || diff >= width/2 || offRight >= offLeft {
		left = start - width/2
	} else {
		diff = max(diff, e.opts.SideScroll)
		if offLeft < 0 {
			left = w.left - diff
		} else {
			left = w.left + diff
		}
	}
	w.left = max(left, 0)
}

// leftChanged moves the cursor onto the screen after the view moved under it, as nvim's leftcol_changed: to the
// last or first column in view, and when the line ends before the view, the view back to its last character.
func (e *Engine) leftChanged(v *view) {
	w := e.window
	width := e.widthOf(w)
	siso := min(e.opts.SideScrollOff, (width-1)/2)
	last := w.left + width - 1
	start, _ := e.cursorCols(v)
	switch {
	case start > last-siso:
		e.cur.Col = v.colAt(e.cur.Line, last-siso, false)
	case start < w.left+siso:
		e.cur.Col = v.colAt(e.cur.Line, w.left+siso, false)
	}
	start, end := e.cursorCols(v)
	switch {
	case end > last:
		e.cur.Col = v.colAt(e.cur.Line, start-1, false)
	case start < w.left:
		if next := v.colAt(e.cur.Line, end+1, false); next != e.cur.Col {
			e.cur.Col = next
		} else {
			w.left = start
		}
	}
	e.setWant(v)
}

// sideScrollCmd runs zl, zh, zL, zH, zs and ze; false when key isn't one of them.
func (e *Engine) sideScrollCmd(v *view, key string, count int) bool {
	w := e.window
	width := e.widthOf(w)
	n := max(count, 1)
	switch key {
	case "zl", "z<Right>", "zh", "z<Left>", "zL", "zH":
		if e.opts.Wrap {
			return true
		}
		switch key {
		case "zl", "z<Right>":
			w.left += n
		case "zh", "z<Left>":
			w.left = max(w.left-n, 0)
		case "zL":
			w.left += width / 2
		case "zH":
			w.left = max(w.left-width/2, 0)
		}
		e.leftChanged(v)
	case "zs", "ze":
		if e.opts.Wrap {
			return true
		}
		siso := min(e.opts.SideScrollOff, (width-1)/2)
		start, end := e.cursorCols(v)
		if key == "zs" {
			w.left = max(start-siso, 0)
		} else if end+siso < width {
			w.left = 0
		} else {
			w.left = end + siso - width + 1
		}
	default:
		return false
	}
	return true
}
