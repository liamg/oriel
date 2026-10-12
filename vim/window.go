package vim

import (
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/text"
)

// frame is a node of the window layout, as vim's: a window, or a row or column of frames.
// Oriel's layout tree (FW-9) shows it; the engine keeps it so window commands mean what they mean in vim.
type frame struct {
	win      *window
	vertical bool // children side by side (a row) rather than stacked (a column)
	children []*frame
	parent   *frame
}

// Window sizes for CTRL-W h/j/k/l: the screen the layout divides, as nvim's default grid.
const screenWidth, screenHeight = 80, 22

func (f *frame) leaves(out []*window) []*window {
	if f.win != nil {
		return append(out, f.win)
	}
	for _, c := range f.children {
		out = c.leaves(out)
	}
	return out
}

func (e *Engine) frameOf(w *window) *frame {
	var find func(f *frame) *frame
	find = func(f *frame) *frame {
		if f.win == w {
			return f
		}
		for _, c := range f.children {
			if r := find(c); r != nil {
				return r
			}
		}
		return nil
	}
	return find(e.layout)
}

// order is the windows in the order CTRL-W w visits them: top-left to bottom-right.
func (e *Engine) order() []*window { return e.layout.leaves(nil) }

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
	f := e.frameOf(old)
	nf := &frame{win: w}
	if p := f.parent; p != nil && p.vertical == vertical {
		i := slices.Index(p.children, f)
		nf.parent = p
		p.children = slices.Insert(p.children, i, nf)
	} else {
		// Turn the leaf into a row or column holding the new window and the old one.
		oldLeaf := &frame{win: old, parent: f}
		nf.parent = f
		f.win = nil
		f.vertical = vertical
		f.children = []*frame{nf, oldLeaf}
	}
	e.enter(w)
}

// closeWindow closes w; the window after it in its row or column, else the one before, gets the space and,
// when w was current, the cursor.
func (e *Engine) closeWindow(w *window) bool {
	if len(e.order()) == 1 {
		e.echo("E444: Cannot close last window")
		return false
	}
	f := e.frameOf(w)
	p := f.parent
	i := slices.Index(p.children, f)
	var heir *frame
	if i+1 < len(p.children) {
		heir = p.children[i+1]
	} else {
		heir = p.children[i-1]
	}
	p.children = slices.Delete(p.children, i, i+1)
	if len(p.children) == 1 {
		// A row or column of one is that one.
		only := p.children[0]
		p.win, p.vertical, p.children = only.win, only.vertical, only.children
		for _, c := range p.children {
			c.parent = p
		}
		if heir == only {
			heir = p
		}
	}
	e.windows = slices.DeleteFunc(e.windows, func(x *window) bool { return x == w })
	if w == e.window {
		next := heir.leaves(nil)[0]
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
		e.echo("Already only one window")
		return
	}
	e.layout = &frame{win: e.window}
	e.windows = []*window{e.window}
	e.prevWin = nil
}

type rect struct{ x, y, w, h int }

// geometry lays the frames out over the screen, dividing each row or column equally as 'equalalways' does,
// with a separator column between side-by-side windows and a status line under each.
func (e *Engine) geometry() map[*window]rect {
	out := map[*window]rect{}
	var lay func(f *frame, r rect)
	lay = func(f *frame, r rect) {
		if f.win != nil {
			out[f.win] = r
			return
		}
		n := len(f.children)
		if f.vertical {
			avail := r.w - (n - 1)
			x := r.x
			for i, c := range f.children {
				w := avail / n
				if i < avail%n {
					w++
				}
				lay(c, rect{x, r.y, w, r.h})
				x += w + 1
			}
			return
		}
		avail := r.h - n
		y := r.y
		for i, c := range f.children {
			h := avail / n
			if i < avail%n {
				h++
			}
			lay(c, rect{r.x, y, r.w, h})
			y += h + 1
		}
	}
	lay(e.layout, rect{0, 0, screenWidth, screenHeight})
	return out
}

// neighbour is the window count steps away in a direction, as CTRL-W h/j/k/l pick it: the window across
// the edge that holds the cursor's screen row or column.
func (e *Engine) neighbour(dir byte, count int) *window {
	g := e.geometry()
	cur := e.window
	for range max(count, 1) {
		r := g[cur]
		v := newView(cur.buf.text.Snapshot())
		cx := r.x + min(v.vcol(cur.cur.Line, cur.cur.Col), r.w-1)
		cy := r.y
		var best *window
		for w, wr := range g {
			if w == cur {
				continue
			}
			var hit bool
			switch dir {
			case 'h':
				hit = wr.x+wr.w+1 == r.x && cy >= wr.y && cy <= wr.y+wr.h
			case 'l':
				hit = r.x+r.w+1 == wr.x && cy >= wr.y && cy <= wr.y+wr.h
			case 'k':
				hit = wr.y+wr.h+1 == r.y && cx >= wr.x && cx <= wr.x+wr.w
			case 'j':
				hit = r.y+r.h+1 == wr.y && cx >= wr.x && cx <= wr.x+wr.w
			}
			if hit {
				best = w
				break
			}
		}
		if best == nil {
			break
		}
		cur = best
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
			e.echo("E23: No alternate file")
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
	f := e.frameOf(e.window)
	p := f.parent
	if p == nil {
		return
	}
	i := slices.Index(p.children, f)
	j := i + 1
	if count > 0 {
		j = count - 1
	} else if j >= len(p.children) {
		j = i - 1
	}
	if j < 0 || j >= len(p.children) || p.children[j].win == nil {
		return
	}
	other := p.children[j]
	f.win, other.win = other.win, f.win
	// The cursor stays in the same place on the screen: the window now there.
	e.enter(f.win)
}

// rotate moves the windows of the current row or column one place down or right (R: up or left).
func (e *Engine) rotate(back bool, count int) {
	f := e.frameOf(e.window)
	p := f.parent
	if p == nil {
		return
	}
	for _, c := range p.children {
		if c.win == nil {
			e.echo("E443: Cannot rotate when another window is split")
			return
		}
	}
	for range count {
		ws := make([]*window, len(p.children))
		for i, c := range p.children {
			ws[i] = c.win
		}
		n := len(ws)
		for i := range p.children {
			if back {
				p.children[i].win = ws[(i+1)%n]
			} else {
				p.children[i].win = ws[(i+n-1)%n]
			}
		}
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
