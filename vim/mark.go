package vim

import (
	"bytes"
	"slices"

	"github.com/liamg/oriel/text"
)

// jump is a jumplist entry: a position in a buffer.
type jump struct {
	Pos
	buf *buffer
}

// setPcmark remembers the cursor as the previous context mark and adds it to the jumplist.
func (e *Engine) setPcmark() {
	if e.pcmark != nil {
		p := *e.pcmark
		e.prevPcmark = &p
	}
	p := e.cur
	e.pcmark = &p
	e.jumps = append(e.jumps, jump{p, e.buffer})
	e.jumpIdx = len(e.jumps)
}

// resetPcmark starts the previous context mark at the first line, as editing another buffer does.
func (e *Engine) resetPcmark() {
	e.pcmark, e.prevPcmark = &Pos{}, nil
}

func (e *Engine) markPos(r rune) (Pos, bool) {
	switch r {
	case '\'', '`':
		if e.pcmark == nil {
			return Pos{}, true
		}
		return *e.pcmark, true
	}
	p, ok := e.marks[r]
	return p, ok
}

func (e *Engine) setMark(r rune) {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '[', r == ']', r == '<', r == '>':
		e.marks[r] = e.cur
	case r == '\'' || r == '`':
		e.setPcmark()
	}
}

// Marks returns the position of each set mark among names, for snapshots: ' and ` read the previous context mark.
func (e *Engine) Mark(r rune) (Pos, bool) {
	switch r {
	case '\'', '`':
		if e.pcmark == nil {
			return Pos{}, false
		}
		return *e.pcmark, true
	case '"':
		return e.buffer.last, true
	}
	return e.markPos(r)
}

// Jump is a jumplist entry: a position in a buffer.
type Jump struct {
	Pos
	Buf int // the buffer's number
}

// Jumps returns the jumplist, oldest first, without duplicate lines, as :jumps shows it.
func (e *Engine) Jumps() []Jump {
	e.cleanJumps()
	var out []Jump
	for _, j := range e.jumps {
		out = append(out, Jump{j.Pos, j.buf.num})
	}
	// nvim hides the newest entry when it is on the cursor's line.
	if n := len(e.jumps); n > 0 && e.jumpIdx == n && e.jumps[n-1].buf == e.buffer && e.jumps[n-1].Line == e.cur.Line {
		out = out[:n-1]
	}
	return out
}

// cleanJumps drops entries whose line appears again later in the list, and those in buffers that were
// unloaded or wiped out, as nvim's 'jumpoptions' "clean" does.
func (e *Engine) cleanJumps() {
	var out []jump
	idx := e.jumpIdx
	for i, j := range e.jumps {
		if i == e.jumpIdx {
			idx = len(out)
		}
		drop := j.buf.unloaded || !slices.Contains(e.buffers, j.buf)
		for _, k := range e.jumps[i+1:] {
			if k.buf == j.buf && k.Line == j.Line {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, j)
		}
	}
	if e.jumpIdx >= len(e.jumps) {
		idx = len(out)
	}
	e.jumps, e.jumpIdx = out, idx
}

// CanJump says whether CTRL-O (n < 0) or CTRL-I (n > 0) would go to another jumplist entry.
func (e *Engine) CanJump(n int) bool {
	e.cleanJumps()
	idx, size := e.jumpIdx, len(e.jumps)
	if idx == size {
		if n > 0 {
			return false
		}
		// CTRL-O adds the cursor first, which drops an entry on its line.
		size = 1
		for _, j := range e.jumps {
			if j.buf != e.buffer || j.Line != e.cur.Line {
				size++
			}
		}
		idx = size - 1
	}
	return idx+n >= 0 && idx+n < size
}

// jump is CTRL-O (n < 0) and CTRL-I (n > 0). An entry in another buffer edits that buffer.
func (e *Engine) jump(v *view, n int) {
	e.cleanJumps()
	if len(e.jumps) == 0 {
		return
	}
	if e.jumpIdx+n < 0 || e.jumpIdx+n >= len(e.jumps) {
		return
	}
	if e.jumpIdx == len(e.jumps) {
		e.setPcmark()
		e.cleanJumps()
		e.jumpIdx = len(e.jumps) - 1
		if e.jumpIdx+n < 0 {
			return
		}
	}
	e.jumpIdx += n
	j := e.jumps[e.jumpIdx]
	if j.buf != e.buffer {
		if err := e.showBuffer(j.buf, false); err != nil {
			e.fail(err)
			return
		}
		e.resetPcmark()
		v = e.view()
	}
	e.cur = Pos{min(j.Line, v.lines()-1), j.Col}
	e.clamp(v)
	e.setWant(v)
}

// adjustMarks moves a buffer's marks, the jumplist entries in it and the cursors of the windows showing it
// through a change to it, the way vim's mark_adjust does: by whole lines. A mark on a line that is deleted
// is deleted. The current window's cursor is left to the command that made the change.
func (e *Engine) adjustMarks(buf *buffer, ch text.Change) {
	before := ch.Before
	for i, r := range ch.Replaces {
		a := before.PosAt(r.Range.Start)
		b := before.PosAt(r.Range.End)
		added := bytes.Count(r.Text, []byte("\n"))
		removed := b.Line - a.Line
		if added == 0 && removed == 0 {
			continue
		}
		// Whole lines go when the replace starts at a line start and ends at one.
		first, last := a.Line+1, b.Line
		if a.Col == 0 && b.Col == 0 {
			first, last = a.Line, b.Line-1
		}
		if a.Col == 0 && removed > 0 && r.Range.End == before.Len() && b.Col > 0 {
			first, last = a.Line, b.Line
		}
		if a.Col > 0 && r.Range.End == before.Len() && removed > 0 && bytes.Count(before.Slice(r.Range), []byte("\n")) == removed && a.Col == len(before.Line(a.Line)) {
			first, last = a.Line+1, b.Line
		}
		delta := added - removed
		move := func(p Pos) (Pos, bool) {
			switch {
			case p.Line >= first && p.Line <= last && removed > 0:
				return p, false
			case p.Line > last || p.Line >= first && removed == 0:
				p.Line += delta
			}
			return p, true
		}
		for k, p := range buf.marks {
			if np, ok := move(p); ok {
				buf.marks[k] = np
			} else if k >= 'a' && k <= 'z' || k >= 'A' && k <= 'Z' {
				if ch.Cause == text.CauseEdit {
					seq := buf.text.UndoSeq()
					if buf.undoMarks[seq] == nil {
						buf.undoMarks[seq] = map[rune]Pos{}
					}
					buf.undoMarks[seq][k] = p
				}
				delete(buf.marks, k)
			}
		}
		for _, w := range e.windows {
			for i, j := range w.jumps {
				if np, ok := move(j.Pos); ok && j.buf == buf {
					w.jumps[i].Pos = np
				}
			}
			if w.buf != buf {
				continue
			}
			if removed > 0 {
				n := last - first + 1
				w.fold.adjustFolds(first, n, max(delta+n, 0))
			} else {
				w.fold.adjustFolds(first, 0, added)
			}
			if w.pcmark != nil {
				if np, ok := move(*w.pcmark); ok {
					w.pcmark = &np
				}
			}
			if w != e.window {
				np, ok := move(w.cur)
				if !ok {
					np = Pos{min(first, max(0, before.Lines()-1+delta-1)), 0}
				}
				w.cur = np
			}
		}
		if buf == e.buffer {
			for _, m := range e.gstack {
				for i, l := range m.lines {
					if l < 0 {
						continue
					}
					if np, ok := move(Pos{l, 0}); ok {
						m.lines[i] = np.Line
					} else {
						m.lines[i] = -1
					}
				}
			}
		}
		if buf.uLine >= 0 {
			if np, ok := move(Pos{buf.uLine, 0}); ok {
				buf.uLine = np.Line
			} else {
				buf.uLine = -1
			}
		}
		if i+1 < len(ch.Replaces) {
			before = applyReplace(before, r)
		}
	}
	for _, w := range e.windows {
		if w.buf == buf {
			e.updateFolds(w)
		}
	}
}

// undo is u (redo false) and CTRL-R, putting the cursor where vim does.
func (e *Engine) undo(redo bool) {
	seq := e.ed.UndoSeq()
	var ch text.Change
	var ok bool
	if redo {
		ch, ok = e.ed.Redo()
		seq = e.ed.UndoSeq()
	} else {
		ch, ok = e.ed.Undo()
	}
	if !ok {
		if redo {
			e.notify(msgNewestChange.with())
		} else {
			e.notify(msgOldestChange.with())
		}
		return
	}
	e.cursorAfterUndo(ch, seq)
	if marks, ok := e.undoMarks[seq]; ok && !redo {
		for k, p := range marks {
			e.marks[k] = p
		}
	}
}

// undoTime is g- and g+: move n steps back or forward in the order the changes were made, whatever branch
// they are on. The cursor goes where the last undo or redo on the way leaves it.
func (e *Engine) undoTime(forward bool, n int) {
	seq := e.ed.UndoSeq()
	target := seq - n
	if forward {
		target = seq + n
	}
	if target < 0 {
		target = 0
	}
	if target == seq {
		return
	}
	ancestors := map[int]bool{}
	for s := seq; ; {
		ancestors[s] = true
		p, ok := e.ed.UndoParent(s)
		if !ok {
			break
		}
		s = p
	}
	last := target
	if ancestors[target] {
		// Only undoing: the last step undone is the one just above target.
		for s := seq; s != target; {
			p, _ := e.ed.UndoParent(s)
			if p == target {
				last = s
			}
			s = p
		}
	}
	ch, err := e.ed.UndoTo(target)
	if err != nil {
		return
	}
	e.cursorAfterUndo(ch, last)
}

// cursorAfterUndo follows vim's u_undoredo(): the cursor goes to the cursor saved when the step was made if
// that was near the change, else to the first changed line.
func (e *Engine) cursorAfterUndo(ch text.Change, seq int) {
	v := e.view()
	if len(ch.Replaces) == 0 {
		return
	}
	top, bottom := -1, -1
	s := ch.Before
	for _, r := range ch.Replaces {
		a := s.PosAt(r.Range.Start)
		next := applyReplace(s, r)
		_ = next
		l0 := a.Line
		l1 := l0 + bytes.Count(r.Text, []byte("\n"))
		if top < 0 || l0 < top {
			top = l0
		}
		bottom = max(bottom, l1)
		s = next
	}
	saved, hasSaved := e.undoCur[seq]
	line := top
	if hasSaved && saved.Line >= top-1 && saved.Line <= bottom+1 {
		line = saved.Line
	}
	if hasSaved && saved.Line+1 == line && line > 0 {
		line--
	}
	line = min(line, v.lines()-1)
	e.cur.Line = line
	if hasSaved && saved.Line == line {
		e.cur.Col = saved.Col
	} else {
		e.cur.Col = v.colAt(line, e.want, false)
	}
	e.clamp(v)
	e.setWant(v)
}

// applyReplace returns the snapshot after one replace, for walking a change's replaces in order.
func applyReplace(s text.Snapshot, r text.Replace) text.Snapshot {
	b := text.New(s.Bytes(), text.Author{})
	ch, err := b.Apply(text.Edit{Ranges: []text.Replace{r}})
	if err != nil {
		return s
	}
	return ch.After
}

// Register reads a register as getreg() does: "" reads the register it points at, and linewise text ends in a newline.
func (e *Engine) Register(r rune) (string, bool) {
	switch r {
	case '"':
		if e.unnamed == 0 {
			return "", false
		}
		r = e.unnamed
	case '.':
		return e.inserted, e.inserted != "" || e.dot != nil
	}
	v, ok := e.ed.Register(r)
	return v.Text, ok
}

// saveULine remembers a line before its first change, for U. Changes that add or remove lines don't count.
func (e *Engine) saveULine(ch text.Change) {
	if e.inU || len(ch.Replaces) == 0 {
		return
	}
	line := -1
	for _, r := range ch.Replaces {
		if bytes.IndexByte(r.Text, '\n') >= 0 || bytes.IndexByte(ch.Before.Slice(r.Range), '\n') >= 0 {
			return
		}
		l := ch.Before.PosAt(r.Range.Start).Line
		if line >= 0 && l != line {
			return
		}
		line = l
	}
	if line == e.uLine {
		return
	}
	e.uLine = line
	e.uText = string(ch.Before.Line(line))
	e.uCol = 0
	if e.changeCursor.Line == line {
		e.uCol = e.changeCursor.Col
	}
}

// undoLine is U: put back the last changed line as it was before its changes. U is itself a change.
func (e *Engine) undoLine() {
	v := e.view()
	if e.uLine < 0 || e.uLine >= v.lines() {
		return
	}
	cur := string(v.line(e.uLine))
	col := e.uCol
	if e.cur.Line == e.uLine {
		e.uCol = e.cur.Col
	}
	start := v.s.LineStart(e.uLine)
	e.cur = Pos{e.uLine, col}
	e.inU = true
	e.do(e.newGroup(), text.Replace{Range: text.Range{Start: start, End: start + len(cur)}, Text: []byte(e.uText)})
	e.inU = false
	e.uText = cur
	e.clamp(e.view())
	e.setWant(e.view())
}

// Visual returns the start of the Visual area, while in Visual mode.
func (e *Engine) Visual() (Pos, bool) {
	if e.visual == nil {
		return Pos{}, false
	}
	return *e.visual, true
}

// SetVisual starts charwise Visual mode with the area running from start to the cursor.
func (e *Engine) SetVisual(start Pos) {
	e.visual = &start
	e.vmode = 'v'
}
