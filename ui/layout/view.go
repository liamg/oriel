package layout

import (
	"cmp"
	"math"
	"slices"
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tTabStrip   = theme.Declare("pane.tabs.bg", "bg.surface")
	tTabActive  = theme.Declare("pane.tab.active.bg", "bg.raised")
	tTabEdge    = theme.Declare("pane.tab.active.edge", "accent")
	tTabFg      = theme.Declare("pane.tab.fg", "fg.faint")
	tTabFgOn    = theme.Declare("pane.tab.active.fg", "fg")
	tPaneBg     = theme.Declare("pane.bg", "bg.base")
	tDockBg     = theme.Declare("dock.bg", "bg.surface")
	tDivider    = theme.Declare("pane.divider", "line")
	tDrop       = theme.DeclareOn("drop.target.bg", "alpha(accent, 12%)", "bg.base")
	tDropEdge   = theme.Declare("drop.target.edge", "accent")
	tFloatEdge  = theme.Declare("float.border", "line.strong")
	tFocusEdge  = theme.Declare("pane.focus.edge", "accent")
	tDropDownBg = theme.Declare("dropdown.bg", "bg.surface")
)

// Help ids for the layout's parts.
const (
	HelpPane     = "layout/panes"
	HelpTab      = "layout/panes#tabs"
	HelpSplitter = "layout/panes#splits"
	HelpDrop     = "layout/panes#drag-and-drop"
)

// Content builds a view's content into its pane.
type Content func(c *ui.Ctx, v ViewRef, focused bool)

const tabH = 30
const dragSlop = 4
const edgeBand = 24 // px from a window edge that docks a dropped view

// drag is a tab being dragged.
type drag struct {
	view    ViewRef
	from    render.Point
	moving  bool
	at      render.Point
	target  Target
	targetR render.Rect
	ok      bool
}

// viewState is what View keeps between frames.
type viewState struct {
	drag   *drag
	leaves map[*Node]ui.ID // this frame's leaves, for drop targets and ⌃W h/j/k/l
	docks  map[Edge]ui.ID
	resize *resize
}

type resize struct {
	split *Node
	i     int // the splitter between kid i and i+1
	from  render.Point
	w0    []float32
	span  float32
}

// View draws r: docks, main's splits and tab groups, floats and the drop-down, with each pane's content
// from content. Tabs are dragged onto drop targets; splitters are dragged to resize. It records leaves in
// panes, and returns true when r changed.
func View(c *ui.Ctx, key any, r *Root, panes *Panes, content Content) bool {
	id := c.Next(key)
	st := ui.State[viewState](c, id)
	st.leaves, st.docks = map[*Node]ui.ID{}, map[Edge]ui.ID{}
	if panes != nil {
		panes.leaves = st.leaves
	}
	changed := false
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tPaneBg), Help: HelpPane}, func(c *ui.Ctx) {
		if r.Zoomed != nil {
			if w := r.Find(*r.Zoomed); w != nil {
				c.Column("zoomed", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
					content(c, *r.Zoomed, true)
				})
				return
			}
			r.Zoomed = nil
		}
		c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			changed = dock(c, r, st, Left, content) || changed
			c.Column("centre", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				c.Column("main", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
					changed = node(c, r, st, r.Main, content) || changed
				})
				changed = dock(c, r, st, Bottom, content) || changed
			})
			changed = dock(c, r, st, Right, content) || changed
		})
		for i, f := range r.Floats {
			c.Column(fk{i}, ui.Style{W: ui.Px(f.Rect.Dx()), H: ui.Px(f.Rect.Dy()), Z: 500 + i, Fill: th.C(tDockBg), Stroke: th.C(tFloatEdge),
				Border: 1, Radius: 8, Clip: true, Float: &ui.Float{Point: &f.Rect.Min}}, func(c *ui.Ctx) {
				changed = leaf(c, r, st, f.Leaf, content) || changed
			})
		}
		if r.DropDown.Open && len(r.DropDown.Leaf.Tabs) > 0 {
			size := c.Size()
			c.Column("dropdown", ui.Style{W: ui.Px(size.X), H: ui.Px(r.DropDown.Size), Z: 600, Fill: th.C(tDropDownBg),
				Stroke: th.C(tFloatEdge), Border: 1, Float: &ui.Float{Point: &render.Point{}}}, func(c *ui.Ctx) {
				changed = leaf(c, r, st, r.DropDown.Leaf, content) || changed
			})
		}
		if st.drag != nil && st.drag.moving && st.drag.ok {
			indicator(c, st.drag)
		}
	})
	return changed
}

type fk struct{ i int }

func dock(c *ui.Ctx, r *Root, st *viewState, e Edge, content Content) bool {
	d := r.Docks[e]
	if !d.Open || len(d.Leaf.Tabs) == 0 {
		return false
	}
	s := ui.Style{W: ui.Px(d.Size), H: ui.Flex(1), Fill: c.Theme().C(tDockBg)}
	if e == Bottom {
		s = ui.Style{W: ui.Flex(1), H: ui.Px(d.Size), Fill: c.Theme().C(tDockBg)}
	}
	changed := false
	st.docks[e] = c.Column("dock-"+string(e), s, func(c *ui.Ctx) {
		changed = leaf(c, r, st, d.Leaf, content)
	})
	return changed
}

func node(c *ui.Ctx, r *Root, st *viewState, n *Node, content Content) bool {
	if n.leaf() {
		return leaf(c, r, st, n, content)
	}
	changed := false
	box := c.Column
	main := func(w float32) ui.Style { return ui.Style{W: ui.Flex(w), H: ui.Flex(1)} }
	bar := ui.Style{W: ui.Px(1), H: ui.Flex(1), Cursor: render.CursorResizeCol}
	if n.Split == Horizontal {
		box = c.Row
	} else {
		main = func(w float32) ui.Style { return ui.Style{W: ui.Flex(1), H: ui.Flex(w)} }
		bar = ui.Style{W: ui.Flex(1), H: ui.Px(1), Cursor: render.CursorResizeRow}
	}
	id := box(n, ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
		for i, k := range n.Kids {
			if i > 0 {
				changed = splitter(c, st, n, i-1, bar) || changed
			}
			c.Column(i, main(n.Weights[i]), func(c *ui.Ctx) {
				changed = node(c, r, st, k, content) || changed
			})
		}
	})
	if st.resize != nil && st.resize.split == n {
		if rc, ok := c.Rect(id); ok {
			st.resize.span = rc.Dx()
			if n.Split == Vertical {
				st.resize.span = rc.Dy()
			}
		}
	}
	return changed
}

// splitter is the 1px line between two kids, with a grab area either side of it, dragged to move the split.
func splitter(c *ui.Ctx, st *viewState, n *Node, i int, s ui.Style) bool {
	key := struct{ i int }{i}
	id := c.Next(key)
	in := c.Interaction(id)
	changed := false
	if in.Held {
		if st.resize == nil {
			st.resize = &resize{split: n, i: i, from: in.Pointer, w0: append([]float32(nil), n.Weights...)}
		}
		rs := st.resize
		if rs.split == n && rs.i == i && rs.span > 0 {
			d := in.Pointer.X - rs.from.X
			if n.Split == Vertical {
				d = in.Pointer.Y - rs.from.Y
			}
			var total float32
			for _, w := range rs.w0 {
				total += w
			}
			delta := d / rs.span * total
			a, b := rs.w0[i]+delta, rs.w0[i+1]-delta
			if lo := total * 0.05; a >= lo && b >= lo {
				n.Weights[i], n.Weights[i+1] = a, b
				changed = true
			}
		}
	} else if st.resize != nil && st.resize.split == n && st.resize.i == i {
		st.resize = nil
	}
	s.Fill = c.Theme().C(tDivider)
	s.Interactive, s.Help = true, HelpSplitter
	c.Box(key, s, nil)
	return changed
}

func leaf(c *ui.Ctx, r *Root, st *viewState, l *Node, content Content) bool {
	changed := false
	th := c.Theme()
	focused := r.Focus != nil && l.Active < len(l.Tabs) && l.Tabs[l.Active] == *r.Focus
	st.leaves[l] = c.Column(l, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
		c.Row("tabs", ui.Style{W: ui.Flex(1), H: ui.Px(tabH), Fill: th.C(tTabStrip)}, func(c *ui.Ctx) {
			for i, v := range l.Tabs {
				if tab(c, r, st, l, i, v) {
					changed = true
				}
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tDivider)}, nil)
		if len(l.Tabs) > 0 {
			v := l.Tabs[l.Active]
			body := c.Next("content")
			if in := c.Interaction(body); in.Pressed && (r.Focus == nil || *r.Focus != v) {
				r.Focus = &v
				changed = true
			}
			c.Column("content", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Clip: true, Interactive: true, Help: HelpPane}, func(c *ui.Ctx) {
				content(c, v, focused)
			})
		}
	})
	return changed
}

// tab is one tab: a click activates it, and dragging it past a few pixels starts a move.
func tab(c *ui.Ctx, r *Root, st *viewState, l *Node, i int, v ViewRef) bool {
	th := c.Theme()
	id := c.Next(v)
	in := c.Interaction(id)
	changed := false
	switch {
	case in.Held:
		if st.drag == nil {
			st.drag = &drag{view: v, from: in.Pointer}
		}
		if d := st.drag; d.view == v {
			d.at = in.Pointer
			if !d.moving && math.Hypot(float64(d.at.X-d.from.X), float64(d.at.Y-d.from.Y)) > dragSlop {
				d.moving = true
			}
		}
	case st.drag != nil && st.drag.view == v:
		d := st.drag
		st.drag = nil
		if d.moving && d.ok {
			r.Drop(v, d.target)
			return true
		}
		if in.Clicked {
			l.Active, r.Focus = i, &v
			changed = true
		}
	case in.Clicked:
		l.Active, r.Focus = i, &v
		changed = true
	}
	on := i == l.Active
	fill, fg, edge := render.Color{}, th.C(tTabFg), render.Color{}
	if on {
		fill, fg = th.C(tTabActive), th.C(tTabFgOn)
		if r.Focus != nil && *r.Focus == v {
			edge = th.C(tTabEdge)
		}
	}
	title := v.Title
	if title == "" {
		title = v.String()
	}
	c.Column(v, ui.Style{H: ui.Flex(1), Fill: fill, Interactive: true, Cursor: render.CursorPointer, Help: HelpTab}, func(c *ui.Ctx) {
		c.Box("edge", ui.Style{W: ui.Flex(1), H: ui.Px(2), Fill: edge}, nil)
		c.Row("label", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(14, 0), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: fg})
		})
	})
	if st.drag != nil && st.drag.view == v && st.drag.moving {
		st.drag.ok, st.drag.target, st.drag.targetR = target(c, r, st, st.drag.at)
	}
	return changed
}

// target is where a drop at p would land, and the rect to light up for it.
func target(c *ui.Ctx, r *Root, st *viewState, p render.Point) (bool, Target, render.Rect) {
	size := c.Size()
	switch {
	case p.X < edgeBand:
		return true, Target{Dock: Left}, render.XYWH(0, 0, r.Docks[Left].Size, size.Y)
	case p.X > size.X-edgeBand:
		return true, Target{Dock: Right}, render.XYWH(size.X-r.Docks[Right].Size, 0, r.Docks[Right].Size, size.Y)
	case p.Y > size.Y-edgeBand:
		return true, Target{Dock: Bottom}, render.XYWH(0, size.Y-r.Docks[Bottom].Size, size.X, r.Docks[Bottom].Size)
	}
	for e, id := range st.docks {
		if rc, ok := c.Rect(id); ok && in(p, rc) {
			return true, Target{Dock: e}, rc
		}
	}
	for l, id := range st.leaves {
		rc, ok := c.Rect(id)
		if !ok || !in(p, rc) || r.leafIn(r.Main, firstTab(l)) == nil {
			continue
		}
		var beside *ViewRef
		if len(l.Tabs) > 0 {
			b := l.Tabs[l.Active]
			beside = &b
		}
		fx, fy := (p.X-rc.Min.X)/rc.Dx(), (p.Y-rc.Min.Y)/rc.Dy()
		w, h := rc.Dx(), rc.Dy()
		switch {
		case fx < .25:
			return true, Target{Beside: beside, Zone: ZoneLeft}, render.XYWH(rc.Min.X, rc.Min.Y, w/2, h)
		case fx > .75:
			return true, Target{Beside: beside, Zone: ZoneRight}, render.XYWH(rc.Min.X+w/2, rc.Min.Y, w/2, h)
		case fy < .25:
			return true, Target{Beside: beside, Zone: ZoneTop}, render.XYWH(rc.Min.X, rc.Min.Y, w, h/2)
		case fy > .75:
			return true, Target{Beside: beside, Zone: ZoneBottom}, render.XYWH(rc.Min.X, rc.Min.Y+h/2, w, h/2)
		}
		return true, Target{Beside: beside, Zone: ZoneCenter}, rc
	}
	f := render.XYWH(p.X-200, p.Y-15, 640, 400)
	return true, Target{Float: &f}, f
}

func firstTab(l *Node) ViewRef {
	if len(l.Tabs) == 0 {
		return ViewRef{}
	}
	return l.Tabs[0]
}

func in(p render.Point, r render.Rect) bool {
	return p.X >= r.Min.X && p.X < r.Max.X && p.Y >= r.Min.Y && p.Y < r.Max.Y
}

// indicator lights up a drag's drop target, gliding between targets on springs.
func indicator(c *ui.Ctx, d *drag) {
	th := c.Theme()
	x := c.Anim("x", d.targetR.Min.X)
	y := c.Anim("y", d.targetR.Min.Y)
	w := c.Anim("w", d.targetR.Dx())
	h := c.Anim("h", d.targetR.Dy())
	at := render.Point{X: x, Y: y}
	// The glow sits behind the target, as a box-shadow does; the target's fill covers its inside.
	c.Box("glow", ui.Style{W: ui.Px(w), H: ui.Px(h), Z: 1999, Float: &ui.Float{Point: &at}}, func(c *ui.Ctx) {
		c.Paint("fx", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
			f.Effect(render.Glow, r, 12, theme.WithAlpha(th.C(tDropEdge), 0.35*th.Effect("glow")))
		})
	})
	c.Box("drop", ui.Style{W: ui.Px(w), H: ui.Px(h), Z: 2000, Fill: th.C(tDrop), Stroke: th.C(tDropEdge), Border: 1.5, Radius: 6,
		Float: &ui.Float{Point: &at}, Help: HelpDrop}, nil)
	ghost := render.Point{X: d.at.X + 10, Y: d.at.Y + 10}
	c.Row("ghost", ui.Style{Pad: ui.PadXY(10, 4), Z: 2001, Fill: th.C(tTabActive), Stroke: th.C(tDropEdge), Border: 1, Radius: 4,
		Float: &ui.Float{Point: &ghost}}, func(c *ui.Ctx) {
		title := d.view.Title
		if title == "" {
			title = d.view.String()
		}
		c.Text("t", strings.TrimSpace(title), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: th.C(tTabFgOn)})
	})
}

// Dir is a direction for ⌃W h/j/k/l.
type Dir string

const (
	DirLeft  Dir = "h"
	DirDown  Dir = "j"
	DirUp    Dir = "k"
	DirRight Dir = "l"
)

// FocusDir moves focus to the nearest pane in dir from the focused one, across main and the docks alike, by
// where they were drawn last frame (rects, by leaf).
func (r *Root) FocusDir(d Dir, rects map[*Node]render.Rect) bool {
	if r.Focus == nil {
		return false
	}
	w := r.Find(*r.Focus)
	if w == nil {
		return false
	}
	from, ok := rects[w.Leaf]
	if !ok {
		return false
	}
	cx, cy := (from.Min.X+from.Max.X)/2, (from.Min.Y+from.Max.Y)/2
	type cand struct {
		l *Node
		d float32
		r render.Rect
	}
	var cands []cand
	for l, rc := range rects {
		if l == w.Leaf || len(l.Tabs) == 0 {
			continue
		}
		var gap, off float32
		switch d {
		case DirLeft:
			gap, off = from.Min.X-rc.Max.X, abs(cy-(rc.Min.Y+rc.Max.Y)/2)
		case DirRight:
			gap, off = rc.Min.X-from.Max.X, abs(cy-(rc.Min.Y+rc.Max.Y)/2)
		case DirUp:
			gap, off = from.Min.Y-rc.Max.Y, abs(cx-(rc.Min.X+rc.Max.X)/2)
		case DirDown:
			gap, off = rc.Min.Y-from.Max.Y, abs(cx-(rc.Min.X+rc.Max.X)/2)
		}
		if gap >= -1 {
			cands = append(cands, cand{l, gap*4 + off, rc})
		}
	}
	if len(cands) == 0 {
		return false
	}
	// Nearest first; a tie goes back to where focus came from, then to the leftmost, topmost.
	prev := func(c cand) bool { return r.prev != nil && slices.Contains(c.l.Tabs, *r.prev) }
	slices.SortFunc(cands, func(a, b cand) int {
		switch {
		case abs(a.d-b.d) > 1:
			return cmp.Compare(a.d, b.d)
		case prev(a) != prev(b):
			if prev(a) {
				return -1
			}
			return 1
		case a.r.Min.X != b.r.Min.X:
			return cmp.Compare(a.r.Min.X, b.r.Min.X)
		}
		return cmp.Compare(a.r.Min.Y, b.r.Min.Y)
	})
	best := cands[0].l
	v := best.Tabs[best.Active]
	r.prev, r.Focus = r.Focus, &v
	return true
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// Panes tracks where View drew each leaf, for FocusDir.
type Panes struct{ leaves map[*Node]ui.ID }

// Rects is where each leaf was drawn in the latest frame.
func (p *Panes) Rects(u *ui.UI) map[*Node]render.Rect {
	out := map[*Node]render.Rect{}
	for l, id := range p.leaves {
		if rc, ok := u.Rect(id); ok {
			out[l] = rc
		}
	}
	return out
}

// RectsIn is Rects from inside a frame being built: last frame's rects.
func (p *Panes) RectsIn(c *ui.Ctx) map[*Node]render.Rect {
	out := map[*Node]render.Rect{}
	for l, id := range p.leaves {
		if rc, ok := c.Rect(id); ok {
			out[l] = rc
		}
	}
	return out
}
