package vim

// Scrolling, as vim's move.c does it with 'scrolloff' 0, 'scrolljump' 1 and lines that don't wrap: each line, or
// closed fold, is one screen row.

// SetHeight sets how many rows the current window shows; 0 goes back to its share of the default screen.
func (e *Engine) SetHeight(rows int) {
	e.window.height = rows
	e.updateTop(e.view())
}

// Top is the first line the current window shows.
func (e *Engine) Top() int { return e.top }

// SetTop scrolls the current window to show line first, as the scroll wheel would; the cursor stays in view.
func (e *Engine) SetTop(line int) {
	v := e.view()
	e.top = e.foldStart(min(max(line, 0), v.lines()-1))
	e.keepCursorIn(v)
	e.sync()
}

// Height is how many rows the current window shows.
func (e *Engine) Height() int { return e.heightOf(e.window) }

func (e *Engine) heightOf(w *window) int {
	if w.height > 0 {
		return w.height
	}
	return max(e.layout.Rows(WindowID(w.id)), 1)
}

// scrollAmount is 'scroll': the lines CTRL-D and CTRL-U move, half the window unless a count set it.
func (e *Engine) scrollAmount() int {
	if e.window.scroll > 0 {
		return e.window.scroll
	}
	return max(e.Height()/2, 1)
}

// nextRow is the first line of the row after line's.
func (e *Engine) nextRow(line int) int { return e.foldEndOf(line) + 1 }

// prevRow is the first line of the row before line's.
func (e *Engine) prevRow(line int) int { return e.foldStart(line - 1) }

// botline is the first line below the window, as vim's w_botline: past the end when the end shows.
func (e *Engine) botline(v *view) int {
	l := e.top
	for range e.Height() {
		if l >= v.lines() {
			break
		}
		l = e.nextRow(l)
	}
	return l
}

// emptyRows is how many rows below the last line the window shows ("~" lines).
func (e *Engine) emptyRows(v *view) int {
	rows, l := 0, e.top
	for l < v.lines() && rows < e.Height() {
		l = e.nextRow(l)
		rows++
	}
	return e.Height() - rows
}

// rowsBetween counts the rows from line a up to but not including line b.
func (e *Engine) rowsBetween(a, b int) int {
	n := 0
	for l := a; l < b; l = e.nextRow(l) {
		n++
	}
	return n
}

// updateTop scrolls so the cursor shows, as vim's update_topline: a little way off the window scrolls just enough,
// further away centres the cursor.
func (e *Engine) updateTop(v *view) {
	h := e.Height()
	e.top = e.foldStart(min(max(e.top, 0), v.lines()-1))
	cur := e.foldStart(e.cur.Line)
	if cur < e.top {
		if e.rowsBetween(cur, e.top) >= max(h/2-1, 2) {
			e.halfway(v, false, false)
		} else {
			e.top = cur
		}
		return
	}
	bot := e.botline(v)
	if bot >= v.lines() || cur < bot {
		return
	}
	n := 0
	for l := e.cur.Line; l >= bot; l = e.prevRow(l) {
		n++
		if n > h+1 {
			break
		}
	}
	if n <= h+1 {
		e.cursorToBottom(v, 1)
	} else {
		e.halfway(v, false, false)
	}
}

// cursorToBottom scrolls the fewest lines, and at least minScroll, that bring the cursor's line onto the window's
// last row, as vim's scroll_cursor_bot; when that's a window or more, it centres the cursor instead.
func (e *Engine) cursorToBottom(v *view, minScroll int) {
	h := e.Height()
	bot := e.botline(v)
	cln := e.foldStart(e.cur.Line)
	used, scrolled := 1, 0
	if cln >= bot {
		scrolled = 1
		if cln == bot {
			scrolled -= e.emptyRows(v)
		}
	}
	loff, boff := cln, e.foldEndOf(cln)
	for loff > 0 {
		if (scrolled <= 0 || scrolled >= minScroll || boff+1 > v.lines()-1) && loff <= bot {
			break
		}
		loff = e.prevRow(loff)
		used++
		if used > h {
			break
		}
		if loff >= bot {
			scrolled++
			if loff == bot {
				scrolled -= e.emptyRows(v)
			}
		}
		if boff < v.lines()-1 {
			boff = e.foldEndOf(boff + 1)
			used++
			if used > h {
				break
			}
			if scrolled < minScroll {
				if boff >= bot {
					scrolled++
					if e.foldStart(boff) == bot {
						scrolled -= e.emptyRows(v)
					}
				}
			}
		}
	}
	var count int
	switch {
	case scrolled <= 0:
	case used > h:
		count = used
	default:
		i := 0
		for b := e.top; i < scrolled && b < bot; b = e.nextRow(b) {
			i++
			count++
		}
		if i < scrolled {
			count = 9999
		}
	}
	if count >= h && count > minScroll {
		e.halfway(v, false, true)
	} else if count > 0 {
		e.scrollRows(v, count)
	}
}

// scrollBot puts the cursor's line on the window's last row.
func (e *Engine) scrollBot() {
	top := e.foldStart(e.cur.Line)
	for used := 1; top > 0 && used < e.Height(); used++ {
		top = e.prevRow(top)
	}
	e.top = top
}

// halfway puts the cursor's line in the middle of the window, as scroll_cursor_halfway: a row below, then one
// above, until the window is full; preferAbove takes the row above first. At the end of the buffer, atend counts
// the empty rows below it as used, as zz does.
func (e *Engine) halfway(v *view, atend, preferAbove bool) {
	h := e.Height()
	top := e.foldStart(e.cur.Line)
	below := e.foldEndOf(e.cur.Line)
	used, above, belowRows := 1, 0, 0
	for top > 0 {
		if belowRows < above || belowRows == above && !preferAbove {
			if below < v.lines()-1 {
				below = e.foldEndOf(below + 1)
				if used++; used > h {
					break
				}
			} else if atend {
				used++
			}
			belowRows++
			continue
		}
		if used++; used > h {
			break
		}
		above++
		top = e.prevRow(top)
	}
	e.top = top
}

// keepCursorIn moves the cursor onto the window after it scrolled: to the first line, or the last.
func (e *Engine) keepCursorIn(v *view) {
	moved := false
	if e.cur.Line < e.top {
		e.cur.Line, moved = e.top, true
	} else if bot := e.botline(v); e.cur.Line >= bot {
		e.cur.Line, moved = e.prevRow(bot), true
	}
	if moved {
		e.cur.Col = v.colAt(e.cur.Line, e.want, false)
	}
}

// scrollRows moves the window n rows down the buffer (up when n < 0), never past the last line. It reports
// whether the window moved.
func (e *Engine) scrollRows(v *view, n int) bool {
	before := e.top
	for ; n > 0 && e.foldEndOf(e.top) < v.lines()-1; n-- {
		e.top = e.nextRow(e.top)
	}
	for ; n < 0 && e.top > 0; n++ {
		e.top = e.prevRow(e.top)
	}
	return e.top != before
}

// scrollCmd runs the scrolling commands; false when key isn't one.
func (e *Engine) scrollCmd(v *view, key string, count int) bool {
	switch key {
	case "<C-e>", "<C-y>":
		n := max(count, 1)
		if key == "<C-y>" {
			n = -n
		}
		e.scrollRows(v, n)
		e.keepCursorIn(v)
	case "<C-d>", "<C-u>":
		e.halfPage(v, key == "<C-d>", count)
	case "<C-f>", "<C-b>", "<PageDown>", "<PageUp>", "<S-Down>", "<S-Up>":
		e.page(v, key == "<C-f>" || key == "<PageDown>" || key == "<S-Down>", max(count, 1))
	case "zt", "z<CR>", "zz", "z.", "zb", "z-":
		if count > 0 {
			e.cur.Line = min(count, v.lines()) - 1
		}
		switch key {
		case "zt", "z<CR>":
			e.top = e.foldStart(e.cur.Line)
		case "zz", "z.":
			e.halfway(v, true, false)
		default:
			e.scrollBot()
		}
		if key == "z<CR>" || key == "z." || key == "z-" {
			e.cur.Col = v.firstNonBlank(e.cur.Line)
			e.setWant(v)
		} else if count > 0 {
			e.cur.Col = v.colAt(e.cur.Line, e.want, false)
		}
	default:
		return false
	}
	return true
}

// halfPage is CTRL-D and CTRL-U, as nvim's pagescroll with half set: scroll 'scroll' lines, or fewer so the
// end of the buffer doesn't scroll into view, and move the cursor as many lines.
func (e *Engine) halfPage(v *view, fwd bool, count int) {
	h := e.Height()
	if count > 0 {
		e.window.scroll = min(h, count)
	}
	n := min(h, e.scrollAmount())
	moveBy := n
	prevLine, prevCol := e.cur.Line, e.cur.Col
	if fwd {
		if shown := e.rowsBetween(e.top, v.lines()); shown < h+n {
			n = shown - h
		}
	}
	moved := false
	if n > 0 {
		if !fwd {
			n = -n
		}
		moved = e.scrollRows(v, n)
	}
	line := e.cur.Line
	for range moveBy {
		if fwd {
			if e.foldEndOf(line) >= v.lines()-1 {
				break
			}
			line = e.nextRow(line)
		} else {
			if line <= 0 {
				break
			}
			line = e.prevRow(line)
		}
	}
	e.cur.Line = e.foldStart(line)
	e.cur.Col = v.colAt(e.cur.Line, e.want, false)
	if !moved && e.cur.Line == prevLine && e.cur.Col == prevCol {
		e.failed = true
	}
}

// page is CTRL-F and CTRL-B: count windows less a line or two of overlap, and the cursor to the top (or bottom).
func (e *Engine) page(v *view, fwd bool, count int) {
	moved := e.scrollRows(v, sign(fwd)*count*e.overlap(v, fwd))
	if !moved {
		e.failed = true
		return
	}
	if fwd {
		e.cur.Line = e.top
	} else {
		e.cur.Line = e.prevRow(e.botline(v))
	}
	e.cur.Col = v.colAt(e.cur.Line, e.want, false)
}

func sign(fwd bool) int {
	if fwd {
		return 1
	}
	return -1
}

// overlap is how far CTRL-F and CTRL-B scroll a page, as nvim's get_scroll_overlap: two rows show again, or none
// where the window already shows the end it's moving towards.
func (e *Engine) overlap(v *view, fwd bool) int {
	min := e.Height() - 2
	switch {
	case !fwd && e.top == 0, fwd && e.botline(v) >= v.lines(), min < 2:
		return max(min+2, 1)
	case min == 2:
		return min + 1
	}
	return min
}

// screenLine is H, M and L: a line of the window.
func screenLine(e *Engine, v *view, m motionCtx) (target, bool) {
	var line int
	switch m.key {
	case "L":
		line = e.prevRow(e.botline(v))
		if m.count1()-1 >= line {
			line = 0
		} else {
			for n := m.count1() - 1; n > 0 && line > e.top; n-- {
				line = e.prevRow(line)
			}
		}
	case "M":
		half := (e.Height() - e.emptyRows(v) + 1) / 2
		used, l := 0, e.top
		for l < v.lines()-1 {
			used++
			if used >= half {
				break
			}
			l = e.nextRow(l)
		}
		line = l
	default:
		line = e.top
		for n := m.count1() - 1; n > 0; n-- {
			if e.nextRow(line) >= e.botline(v) {
				line = min(e.top+m.count1()-1, v.lines()-1)
				break
			}
			line = e.nextRow(line)
		}
	}
	return target{pos: Pos{min(line, v.lines()-1), 0}, kind: linewise, jump: true, keepCol: true}, true
}
