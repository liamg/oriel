package vim

import (
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/text"
)

// order is the windows in the order CTRL-W w visits them: top-left to bottom-right.
func (e *Engine) order() []*window {
	var out []*window
	for _, id := range e.layout.Order() {
		if w := e.windowByID(id); w != nil {
			out = append(out, w)
		}
	}
	return out
}

// Windows is how many windows there are, for the UI's layout and for tests.
func (e *Engine) Windows() int { return len(e.order()) }

// enter makes w the current window, keeping the cursor of the one it leaves.
func (e *Engine) enter(w *window) {
	if w == e.window {
		return
	}
	e.prevWin = e.window
	e.window, e.buffer = w, w.buf
	v := e.view()
	e.clamp(v)
	e.layout.Focus(WindowID(w.id))
}

// split opens a new window on the current buffer, above (or left of, for vertical) the current one,
// and makes it current, as :split and :vsplit do with 'splitbelow' and 'splitright' off.
func (e *Engine) split(vertical bool, b *buffer) {
	if b == nil {
		b = e.buffer
	}
	old := e.window
	w := e.newWindow(e.ed.Open(b.text), b)
	if b == e.buffer {
		w.cur, w.want = old.cur, old.want
		w.jumps = slices.Clone(old.jumps)
		w.jumpIdx = old.jumpIdx
		if old.pcmark != nil {
			p := *old.pcmark
			w.pcmark = &p
		}
	}
	w.alt = old.alt
	if old.loc != nil {
		w.loc = old.loc.clone()
	}
	if b == old.buf {
		w.fold = old.fold
		w.fold.folds = cloneFolds(old.fold.folds)
	}
	if b != old.buf {
		w.alt = old.buf
	}
	w.top, w.scroll = old.top, old.scroll
	e.layout.Split(WindowID(old.id), WindowID(w.id), vertical)
	e.enter(w)
}

// closeWindow closes w; the window after it in its row or column, else the one before, gets the space and,
// when w was current, the cursor.
func (e *Engine) closeWindow(w *window) bool {
	if len(e.order()) == 1 {
		e.notify(errLastWindow.with())
		return false
	}
	heir := e.layout.Close(WindowID(w.id))
	e.windows = slices.DeleteFunc(e.windows, func(x *window) bool { return x == w })
	if w == e.window {
		next := e.windowByID(heir)
		if e.prevWin == w {
			e.prevWin = nil
		}
		e.window = nil
		e.enter(next)
	} else if e.prevWin == w {
		e.prevWin = nil
	}
	return true
}

// only closes every window but the current one.
func (e *Engine) only() {
	if len(e.order()) == 1 {
		e.notify(msgOnlyWindow.with())
		return
	}
	e.layout.Only(WindowID(e.window.id))
	e.windows = []*window{e.window}
	e.prevWin = nil
}

// neighbour is the window count steps away in a direction, as CTRL-W h/j/k/l pick it: the window across
// the edge that holds the cursor's screen row or column.
func (e *Engine) neighbour(dir byte, count int) *window {
	cur := e.window
	for range max(count, 1) {
		v := newView(cur.buf.text.Snapshot())
		v.folds = &cur.fold
		row := 0
		for l := cur.top; l < cur.cur.Line; row++ {
			_, end, _ := v.folded(l)
			l = end + 1
		}
		next, ok := e.layout.Neighbour(WindowID(cur.id), dir, row, v.vcol(cur.cur.Line, cur.cur.Col))
		if !ok {
			break
		}
		cur = e.windowByID(next)
	}
	return cur
}

// windowCmd runs a CTRL-W command.
func (e *Engine) windowCmd(k string, count int) {
	order := e.order()
	i := slices.Index(order, e.window)
	switch k {
	case "s", "S", "<C-s>":
		e.split(false, nil)
	case "v", "<C-v>":
		e.split(true, nil)
	case "n", "<C-n>":
		e.newBufferWindow(false)
	case "w", "<C-w>":
		if count > 0 {
			e.enter(order[min(count, len(order))-1])
		} else {
			e.enter(order[(i+1)%len(order)])
		}
	case "W":
		if count > 0 {
			e.enter(order[min(count, len(order))-1])
		} else {
			e.enter(order[(i+len(order)-1)%len(order)])
		}
	case "p", "<C-p>":
		if e.prevWin != nil {
			e.enter(e.prevWin)
		}
	case "t", "<C-t>":
		e.enter(order[0])
	case "b", "<C-b>":
		e.enter(order[len(order)-1])
	case "h", "j", "k", "l", "<C-h>", "<C-j>", "<C-k>", "<C-l>", "<Left>", "<Down>", "<Up>", "<Right>":
		d := map[string]byte{"h": 'h', "<C-h>": 'h', "<Left>": 'h', "j": 'j', "<C-j>": 'j', "<Down>": 'j',
			"k": 'k', "<C-k>": 'k', "<Up>": 'k', "l": 'l', "<C-l>": 'l', "<Right>": 'l'}[k]
		e.enter(e.neighbour(d, count))
	case "c", "<C-c>":
		e.closeWindow(e.window)
	case "q", "<C-q>":
		e.closeWindow(e.window)
	case "o", "<C-o>":
		e.only()
	case "x", "<C-x>":
		e.exchange(count)
	case "^", "<C-^>", "<C-6>":
		alt := e.window.alt
		if count > 0 {
			alt = e.bufferByNum(count)
		}
		if alt == nil {
			e.notify(errNoAlternate.with())
			return
		}
		e.split(false, alt)
		e.cur = alt.last
		e.clamp(e.view())
		e.setWant(e.view())
	case "r", "<C-r>", "R":
		e.rotate(k == "R", max(count, 1))
	}
}

// exchange swaps the current window with the next one in its row or column (the count-th with a count).
func (e *Engine) exchange(count int) {
	if id, ok := e.layout.Exchange(WindowID(e.window.id), count); ok {
		// The cursor stays in the same place on the screen: the window now there.
		e.enter(e.windowByID(id))
	}
}

// rotate moves the windows of the current row or column one place down or right (R: up or left).
func (e *Engine) rotate(back bool, count int) {
	if !e.layout.Rotate(WindowID(e.window.id), back, count) {
		e.notify(errRotateSplit.with())
	}
}

// newBufferWindow is :new and :vnew: a split with a new, empty buffer.
func (e *Engine) newBufferWindow(vertical bool) {
	b := e.newBuffer(text.New(nil, text.Author{}))
	e.split(vertical, b)
	e.cur, e.want = Pos{}, 0
}

// Window is the current window's number, counting from 1 in CTRL-W w order, as winnr().
func (e *Engine) Window() int { return slices.Index(e.order(), e.window) + 1 }

// Buffers lists the listed buffers' numbers, marking the current one '%' and the alternate '#'.
func (e *Engine) Buffers() string {
	var out []string
	for _, b := range e.listed() {
		s := strconv.Itoa(b.num)
		if b == e.buffer {
			s += "%"
		} else if b == e.window.alt {
			s += "#"
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

// Buf is the current window's text.
func (e *Engine) Buf() text.Snapshot { return e.ed.Buf() }
