package vim

import "slices"

// WindowID names a window to the layout.
type WindowID int

// Layout is where the windows are on screen. The IDE gives the engine its own layout tree, so :split
// and CTRL-W change what the UI shows and a drag in the UI is what the engine sees; without one, the engine
// lays its windows out over nvim's default screen, as vim does.
type Layout interface {
	Split(at, w WindowID, vertical bool) // w goes above at, or left of it
	Bottom(w WindowID)                   // w goes below everything, across the whole screen (the quickfix window)
	Close(w WindowID) (heir WindowID)    // the window that gets w's space
	Only(w WindowID)
	Order() []WindowID // top-left to bottom-right, as CTRL-W w visits them
	// Neighbour is the window across w's edge in dir (h, j, k or l) at the cursor's row and column in w.
	Neighbour(w WindowID, dir byte, row, col int) (WindowID, bool)
	Rows(w WindowID) int
	Exchange(w WindowID, count int) (WindowID, bool) // swaps w with the next window in its row or column
	Rotate(w WindowID, back bool, count int) bool    // false when a window in w's row or column is split
	Focus(w WindowID)                                // the engine's current window
}

// SetLayout gives the engine the IDE's layout. The windows it has are placed in it as they are.
func (e *Engine) SetLayout(l Layout) { e.layout = l }

// WindowID is the current window's.
func (e *Engine) WindowID() WindowID { return WindowID(e.window.id) }

func (e *Engine) windowByID(id WindowID) *window {
	for _, w := range e.windows {
		if WindowID(w.id) == id {
			return w
		}
	}
	return nil
}

// frame is a node of the default layout, as vim's: a window, or a row or column of frames.
type frame struct {
	win      WindowID // 0 for a row or column
	vertical bool     // children side by side (a row) rather than stacked (a column)
	children []*frame
	parent   *frame
}

// frames is the layout the engine keeps when the IDE gives it none.
type frames struct{ root *frame }

func newFrames(w WindowID) *frames { return &frames{root: &frame{win: w}} }

// The screen the layout divides, as nvim's default grid: 80 columns, and 23 rows above the command line, each
// window's status line included.
const screenWidth, screenHeight = 80, 23

func (f *frame) leaves(out []WindowID) []WindowID {
	if f.win != 0 {
		return append(out, f.win)
	}
	for _, c := range f.children {
		out = c.leaves(out)
	}
	return out
}

func (fs *frames) find(w WindowID) *frame {
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
	return find(fs.root)
}

func (fs *frames) Order() []WindowID { return fs.root.leaves(nil) }
func (fs *frames) Focus(WindowID)    {}

func (fs *frames) Split(at, w WindowID, vertical bool) {
	f := fs.find(at)
	nf := &frame{win: w}
	if p := f.parent; p != nil && p.vertical == vertical {
		i := slices.Index(p.children, f)
		nf.parent = p
		p.children = slices.Insert(p.children, i, nf)
		return
	}
	// Turn the leaf into a row or column holding the new window and the old one.
	old := &frame{win: at, parent: f}
	nf.parent = f
	f.win = 0
	f.vertical = vertical
	f.children = []*frame{nf, old}
}

func (fs *frames) Bottom(w WindowID) {
	nf := &frame{win: w}
	if fs.root.win == 0 && !fs.root.vertical {
		nf.parent = fs.root
		fs.root.children = append(fs.root.children, nf)
		return
	}
	root := &frame{children: []*frame{fs.root, nf}}
	fs.root.parent, nf.parent = root, root
	fs.root = root
}

// Close takes w out; the window after it in its row or column, else the one before, gets the space.
func (fs *frames) Close(w WindowID) WindowID {
	f := fs.find(w)
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
	return heir.leaves(nil)[0]
}

func (fs *frames) Only(w WindowID) { fs.root = &frame{win: w} }

type rect struct{ x, y, w, h int }

// geometry lays the frames out over the screen, dividing each row or column equally as 'equalalways' does,
// with a separator column between side-by-side windows and a status line under each. A window's rect is its
// text, without its status line.
func (fs *frames) geometry() map[WindowID]rect {
	out := map[WindowID]rect{}
	var lay func(f *frame, r rect)
	lay = func(f *frame, r rect) {
		if f.win != 0 {
			r.h--
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
		y := r.y
		for i, c := range f.children {
			h := r.h / n
			if i < r.h%n {
				h++
			}
			lay(c, rect{r.x, y, r.w, h})
			y += h
		}
	}
	lay(fs.root, rect{0, 0, screenWidth, screenHeight})
	return out
}

func (fs *frames) Rows(w WindowID) int { return max(fs.geometry()[w].h, 1) }

// Cols is how many screen columns window w has.
func (fs *frames) Cols(w WindowID) int { return max(fs.geometry()[w].w, 1) }

// Neighbour is the window across the edge in dir that holds the cursor's screen row or column.
func (fs *frames) Neighbour(from WindowID, dir byte, row, col int) (WindowID, bool) {
	g := fs.geometry()
	r := g[from]
	cx := r.x + min(col, r.w-1)
	cy := r.y + min(row, r.h-1)
	for _, w := range fs.Order() {
		if w == from {
			continue
		}
		wr := g[w]
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
			return w, true
		}
	}
	return 0, false
}

func (fs *frames) Exchange(w WindowID, count int) (WindowID, bool) {
	f := fs.find(w)
	p := f.parent
	if p == nil {
		return 0, false
	}
	i := slices.Index(p.children, f)
	j := i + 1
	if count > 0 {
		j = count - 1
	} else if j >= len(p.children) {
		j = i - 1
	}
	if j < 0 || j >= len(p.children) || p.children[j].win == 0 {
		return 0, false
	}
	other := p.children[j]
	f.win, other.win = other.win, f.win
	return f.win, true
}

func (fs *frames) Rotate(w WindowID, back bool, count int) bool {
	p := fs.find(w).parent
	if p == nil {
		return true
	}
	for _, c := range p.children {
		if c.win == 0 {
			return false
		}
	}
	for range count {
		ws := make([]WindowID, len(p.children))
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
	return true
}
