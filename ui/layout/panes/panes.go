package panes

import (
	"math"
	"slices"
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/ui/widget"
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
	tEdgeHot    = theme.Declare("pane.edge.hot", "accent")
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
type Content func(c *ui.Ctx, v layout.ViewRef, focused bool)

const tabH = 30
const dragSlop = 4
const edgeBand = 24 // px from a window edge that docks a dropped view

// drag is a tab being dragged.
type drag struct {
	view   layout.ViewRef
	from   render.Point
	moving bool
	at     render.Point
	spot   spot
}

// spot is where a drag would drop and how it's shown: the rect lit, the slot a new tab would take (or the caret
// between tabs it would go), the body its content would fill, and the area the label names it in.
type spot struct {
	ok     bool
	target layout.Target
	lit    render.Rect
	slot   render.Rect
	caret  render.Rect
	body   render.Rect
	area   render.Rect
	label  string
}

// tabRow is a leaf's tab strip and its tabs, in order. head is how wide a dock's header would be as a tab, 0 for
// a strip of tabs.
type tabRow struct {
	id, last ui.ID
	tabs     []ui.ID
	head     float32
}

// viewState is what View keeps between frames.
type viewState struct {
	drag   *drag
	leaves map[*layout.Node]ui.ID // this frame's leaves, for drop targets and ⌃W h/j/k/l
	strips map[*layout.Node]tabRow
	tabs   map[ui.ID]layout.ViewRef
	docks  map[layout.Edge]ui.ID
	resize *resize
	edge   *dockResize
	title  func(layout.ViewRef) string
	mark   func(layout.ViewRef) (string, render.Color)
	lead   func(layout.ViewRef) (render.Color, bool)
	tip    func(layout.ViewRef) string
	edits  []func()                 // changes to the tree, made once it's drawn
	bodies map[ui.ID]layout.ViewRef // this frame's panes' content nodes
	focus  *layout.ViewRef          // r.Focus as the last frame left it
	body   ui.ID                    // the focused view's content node in the last frame
	held   ui.ID                    // keyboard focus as the last frame left it
}

// dockResize is a dock's edge being dragged.
type dockResize struct {
	e     layout.Edge
	from  render.Point
	size0 float32
}

type resize struct {
	box  ui.ID // the split's box: by its place in the tree, so a layout made again mid-drag is the same split
	i    int   // the splitter between kid i and i+1
	from render.Point
	w0   []float32
	span float32
}

// View draws r: docks, main's splits and tab groups, floats and the drop-down, with each pane's content
// from content. Tabs are dragged onto drop targets; splitters are dragged to resize. It records leaves in
// panes, and returns true when r changed.
//
// Keyboard focus decides r.Focus: keyboard focus moving into a view's pane focuses that view. A view focused another
// way (opened, a tab clicked, a command) takes keyboard focus once, at its pane's default target (ui.Ctx.FocusInto).
func View(c *ui.Ctx, key any, r *layout.Root, panes *Panes, content Content) bool {
	id := c.Next(key)
	st := ui.State[viewState](c, id)
	st.leaves, st.strips, st.tabs, st.docks = map[*layout.Node]ui.ID{}, map[*layout.Node]tabRow{}, map[ui.ID]layout.ViewRef{}, map[layout.Edge]ui.ID{}
	st.title, st.mark, st.lead, st.tip = nil, nil, nil, nil
	if panes != nil {
		panes.leaves, panes.tabs = st.leaves, st.tabs
		st.title, st.mark, st.lead, st.tip = panes.Title, panes.Mark, panes.Lead, panes.Tip
	}
	changed := false
	if f := c.Focused(); f != st.held && sameRef(r.Focus, st.focus) {
		for body, v := range st.bodies {
			if c.FocusWithin(body) && (r.Focus == nil || *r.Focus != v) {
				r.Focus, st.focus, changed = &v, &v, true
			}
		}
	}
	st.held = c.Focused()
	had := st.body != 0 && c.FocusWithin(st.body)
	st.bodies = map[ui.ID]layout.ViewRef{}
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tPaneBg), Help: HelpPane}, func(c *ui.Ctx) {
		if r.Zoomed != nil {
			if w := r.Find(*r.Zoomed); w != nil {
				body := c.Column("zoomed", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
					content(c, *r.Zoomed, true)
				})
				st.bodies[body] = *r.Zoomed
				return
			}
			r.Zoomed = nil
		}
		c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			changed = dock(c, r, st, layout.Left, content) || changed
			c.Column("centre", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				c.Column("main", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Role: ui.RoleRegion, Label: "Main"}, func(c *ui.Ctx) {
					if panes != nil && panes.Empty != nil && len(r.Shown()) == 0 && st.drag == nil {
						panes.Empty(c)
						return
					}
					changed = node(c, r, st, r.Main, content) || changed
				})
				changed = dock(c, r, st, layout.Bottom, content) || changed
			})
			changed = dock(c, r, st, layout.Right, content) || changed
		})
		for i, f := range r.Floats {
			c.Column(fk{i}, ui.Style{W: ui.Px(f.Rect.Dx()), H: ui.Px(f.Rect.Dy()), Z: ui.LayerPane + i, Fill: th.C(tDockBg), Stroke: th.C(tFloatEdge),
				Border: 1, Radius: 8, Clip: true, Float: &ui.Float{Point: &f.Rect.Min}, Role: ui.RoleRegion, Label: "Floating pane"}, func(c *ui.Ctx) {
				changed = leaf(c, r, st, f.Leaf, content, false) || changed
			})
		}
		if r.DropDown.Open && len(r.DropDown.Leaf.Tabs) > 0 {
			size := c.Size()
			c.Column("dropdown", ui.Style{W: ui.Px(size.X), H: ui.Px(r.DropDown.Size), Z: ui.LayerPane + 50, Fill: th.C(tDropDownBg),
				Stroke: th.C(tFloatEdge), Border: 1, Float: &ui.Float{Point: &render.Point{}}, Role: ui.RoleRegion, Label: "Drop-down"}, func(c *ui.Ctx) {
				changed = leaf(c, r, st, r.DropDown.Leaf, content, false) || changed
			})
		}
		if st.drag != nil && r.Find(st.drag.view) == nil {
			st.drag = nil
		}
		if d := st.drag; d != nil && d.moving {
			d.spot = target(c, r, st, d.at)
			if d.spot.ok {
				indicator(c, st, d)
			}
		}
	})
	enter(c, r, st, had)
	for _, edit := range st.edits {
		edit()
		changed = true
	}
	st.edits = nil
	return changed
}

// enter moves keyboard focus into the focused view's pane when r.Focus moved since last frame, once that pane is
// built, or when the focused view, holding keyboard focus, moved to another pane.
func enter(c *ui.Ctx, r *layout.Root, st *viewState, had bool) {
	var body ui.ID
	if r.Focus != nil {
		for b, v := range st.bodies {
			if v == *r.Focus {
				body = b
			}
		}
	}
	last := st.body
	st.body = body
	if sameRef(r.Focus, st.focus) {
		if had && body != 0 && body != last {
			c.FocusInto(body)
		}
		return
	}
	if r.Focus != nil {
		if body == 0 && r.Find(*r.Focus) != nil {
			return
		}
		if body != 0 {
			c.FocusInto(body)
		}
	}
	st.focus = nil
	if r.Focus != nil {
		f := *r.Focus
		st.focus = &f
	}
}

func sameRef(a, b *layout.ViewRef) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

type fk struct{ i int }

// dock draws a dock, sliding open and shut from its edge on the motion spring: its size grows from 0 and its
// content fades in, and the reverse on closing. A drag resizes it at once.
func dock(c *ui.Ctx, r *layout.Root, st *viewState, e layout.Edge, content Content) bool {
	d := r.Docks[e]
	open := float32(0)
	if d.Open && len(d.Leaf.Tabs) > 0 {
		open = 1
	}
	p := c.Motion(dockKey{e}, open)
	if p == 0 || len(d.Leaf.Tabs) == 0 {
		return false
	}
	size := d.Size * p
	s := ui.Style{W: ui.Px(size), H: ui.Flex(1), Fill: c.Theme().C(tDockBg), Clip: p < 1}
	if e == layout.Bottom {
		s = ui.Style{W: ui.Flex(1), H: ui.Px(size), Fill: c.Theme().C(tDockBg), Clip: p < 1}
	}
	changed := false
	s.Role, s.Label = ui.RoleRegion, dockNames[e]
	st.docks[e] = c.Column("dock-"+string(e), s, func(c *ui.Ctx) {
		// The content keeps its open size while the dock slides, so it doesn't reflow; it fades with it.
		inner := ui.Style{W: ui.Px(d.Size), H: ui.Flex(1), Fade: 1 - p}
		if e == layout.Bottom {
			inner = ui.Style{W: ui.Flex(1), H: ui.Px(d.Size), Fade: 1 - p}
		}
		c.Column("content", inner, func(c *ui.Ctx) {
			changed = leaf(c, r, st, d.Leaf, content, true)
		})
	})
	if p == 1 {
		changed = dockEdge(c, st, e, d) || changed
	}
	return changed
}

// dockMin is the narrowest a dock is dragged to.
const dockMin = 120

// dockEdge is the grab area along a dock's inner edge, dragged to resize it, lit while it's under the pointer.
func dockEdge(c *ui.Ctx, st *viewState, e layout.Edge, d *layout.Dock) bool {
	r, ok := c.Rect(st.docks[e])
	if !ok {
		return false
	}
	const grab = 6
	at, w, h, cur := render.Point{X: r.Max.X - grab/2, Y: r.Min.Y}, float32(grab), r.Dy(), render.CursorResizeCol
	switch e {
	case layout.Right:
		at.X = r.Min.X - grab/2
	case layout.Bottom:
		at, w, h, cur = render.Point{X: r.Min.X, Y: r.Min.Y - grab/2}, r.Dx(), grab, render.CursorResizeRow
	}
	key := dockEdgeKey{e}
	in := c.Interaction(c.Next(key))
	changed := false
	if in.Held {
		if st.edge == nil || st.edge.e != e {
			st.edge = &dockResize{e: e, from: in.Pointer(), size0: d.Size}
		}
		delta := in.Pointer().X - st.edge.from.X
		switch e {
		case layout.Right:
			delta = -delta
		case layout.Bottom:
			delta = st.edge.from.Y - in.Pointer().Y
		}
		limit := c.Size().X * 0.8
		if e == layout.Bottom {
			limit = c.Size().Y * 0.8
		}
		if size := max(dockMin, min(limit, st.edge.size0+delta)); size != d.Size {
			d.Size, changed = size, true
		}
	} else if st.edge != nil && st.edge.e == e {
		st.edge = nil
	}
	line := render.Color{}
	if in.Hovered || in.Held {
		line = c.Theme().C(tEdgeHot)
	}
	c.Box(key, ui.Style{W: ui.Px(w), H: ui.Px(h), Interactive: true, NoHover: true, Cursor: cur, Help: HelpSplitter,
		Z: ui.LayerContent + 1, Float: &ui.Float{Point: &at}, Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
		if e == layout.Bottom {
			c.Box("line", ui.Style{W: ui.Flex(1), H: ui.Px(2), Fill: line}, nil)
		} else {
			c.Box("line", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: line}, nil)
		}
	})
	return changed
}

type dockEdgeKey struct{ e layout.Edge }

type dockKey struct{ e layout.Edge }

func node(c *ui.Ctx, r *layout.Root, st *viewState, n *layout.Node, content Content) bool {
	if n.IsLeaf() {
		return leaf(c, r, st, n, content, false)
	}
	changed := false
	box := c.Column
	main := func(w float32) ui.Style { return ui.Style{W: ui.Flex(w), H: ui.Flex(1)} }
	bar := ui.Style{W: ui.Px(1), H: ui.Flex(1), Cursor: render.CursorResizeCol}
	if n.Split == layout.Horizontal {
		box = c.Row
	} else {
		main = func(w float32) ui.Style { return ui.Style{W: ui.Flex(1), H: ui.Flex(w)} }
		bar = ui.Style{W: ui.Flex(1), H: ui.Px(1), Cursor: render.CursorResizeRow}
	}
	// Keyed by its place, not its node: the layout can be made again while a splitter is dragged.
	id := c.Next("split")
	box("split", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
		for i, k := range n.Kids {
			if i > 0 {
				changed = splitter(c, st, n, id, i-1, bar) || changed
			}
			c.Column(i, main(n.Weights[i]), func(c *ui.Ctx) {
				changed = node(c, r, st, k, content) || changed
			})
		}
	})
	if st.resize != nil && st.resize.box == id {
		if rc, ok := c.Rect(id); ok {
			st.resize.span = rc.Dx()
			if n.Split == layout.Vertical {
				st.resize.span = rc.Dy()
			}
		}
	}
	return changed
}

// splitter is the 1px line between two kids, with a grab area either side of it, dragged to move the split.
func splitter(c *ui.Ctx, st *viewState, n *layout.Node, box ui.ID, i int, s ui.Style) bool {
	key := struct{ i int }{i}
	id := c.Next(key)
	in := c.Interaction(id)
	changed := false
	if in.Held {
		if st.resize == nil {
			st.resize = &resize{box: box, i: i, from: in.Pointer(), w0: append([]float32(nil), n.Weights...)}
		}
		rs := st.resize
		if rs.box == box && rs.i == i && rs.span > 0 && len(n.Weights) == len(rs.w0) {
			d := in.Pointer().X - rs.from.X
			if n.Split == layout.Vertical {
				d = in.Pointer().Y - rs.from.Y
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
	} else if st.resize != nil && st.resize.box == box && st.resize.i == i {
		st.resize = nil
	}
	s.Fill = c.Theme().C(tDivider)
	s.Interactive, s.Help = true, HelpSplitter
	c.Box(key, s, nil)
	return changed
}

func leaf(c *ui.Ctx, r *layout.Root, st *viewState, l *layout.Node, content Content, docked bool) bool {
	changed := false
	th := c.Theme()
	focused := r.Focus != nil && l.Active < len(l.Tabs) && l.Tabs[l.Active] == *r.Focus
	st.leaves[l] = c.Column(l, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
		// A dock showing one view heads it instead: there are no tabs to choose between.
		head := docked && len(l.Tabs) == 1
		role := ui.RoleTabList
		if head {
			role = ui.RoleNone
		}
		strip := ui.Style{W: ui.Flex(1), H: ui.Px(tabH), Fill: th.C(tTabStrip), Role: role}
		tabs := func(c *ui.Ctx) {
			for i, v := range l.Tabs {
				if tab(c, r, st, l, i, v, head) {
					changed = true
				}
			}
		}
		var sid ui.ID
		if head {
			sid = c.Row("tabs", strip, tabs)
		} else {
			// More tabs than fit scroll sideways, the active one brought into view when it changes.
			o := widget.StripOpts{Items: make([]any, len(l.Tabs)), Help: HelpTab}
			for i, v := range l.Tabs {
				o.Items[i] = v
			}
			if l.Active < len(l.Tabs) {
				o.Active = l.Tabs[l.Active]
			}
			sid = widget.Strip(c, "tabs", strip, o, tabs)
		}
		s := st.strips[l]
		s.id = sid
		st.strips[l] = s
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tDivider)}, nil)
		if len(l.Tabs) > 0 {
			v := l.Tabs[l.Active]
			body := c.Next("content")
			st.bodies[body] = v
			if c.PressedWithin(body) && (r.Focus == nil || *r.Focus != v) {
				r.Focus = &v
				changed = true
			}
			if c.PressedWithin(body) && !c.FocusWithin(body) {
				c.FocusInto(body) // a press on what takes no focus focuses the pane
			}
			c.Column("content", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Clip: true, Interactive: true, Help: HelpPane, Role: ui.RoleGroup,
				Label: title(v)}, func(c *ui.Ctx) {
				content(c, v, focused)
			})
		}
	})
	return changed
}

// closeSide is a tab's ✕ button: big enough to see and to hit.
const closeSide = 20

// tab is one tab: a click activates it, dragging it past a few pixels starts a move, and its ✕, shown while the
// pointer is on it, closes it. head draws it as its dock's header instead: the width of the dock, its project and
// its title in capitals, the ✕ at the end.
func tab(c *ui.Ctx, r *layout.Root, st *viewState, l *layout.Node, i int, v layout.ViewRef, head bool) bool {
	th := c.Theme()
	id := c.Next(v)
	st.tabs[id] = v
	s := st.strips[l]
	s.last = id
	s.tabs = append(s.tabs, id)
	st.strips[l] = s
	in := c.Interaction(id)
	changed := false
	switch {
	case in.Held && !r.IsFixed(v):
		if st.drag == nil {
			st.drag = &drag{view: v, from: in.Pointer()}
		}
		if d := st.drag; d.view == v {
			d.at = in.Pointer()
			if !d.moving && math.Hypot(float64(d.at.X-d.from.X), float64(d.at.Y-d.from.Y)) > dragSlop {
				d.moving = true
			}
		}
	case st.drag != nil && st.drag.view == v:
		d := st.drag
		st.drag = nil
		if d.moving && d.spot.ok {
			st.edits = append(st.edits, func() {
				if r.Drop(v, d.spot.target) == nil {
					st.focus = nil // the dropped view takes keyboard focus where it lands
				}
			})
			return false
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
	title := tabName(st, v)
	name := title
	if v.Project != "" {
		title = v.Project + " · " + title
	}
	var lead render.Color
	led := false
	if st.lead != nil {
		lead, led = st.lead(v)
	}
	tip := ""
	if st.tip != nil {
		tip = st.tip(v)
	}
	adv := c.Shaper().Shape(title, tabFont).Advance
	if tip == "" && adv > tabMaxW {
		tip = title
	}
	if head {
		s.head = 14 + min(adv, tabMaxW) + 7 + closeSide + 6
		if led {
			s.head += 7 + 7
		}
		st.strips[l] = s
	}
	var mark string
	var markTone render.Color
	if st.mark != nil {
		mark, markTone = st.mark(v)
	}
	closed := false
	tabStyle := ui.Style{H: ui.Flex(1), Fill: fill, Interactive: true, Cursor: render.CursorPointer, Help: HelpTab, Role: ui.RoleTab, Selected: on,
		Tooltip: tip}
	if head {
		tabStyle.W, tabStyle.Fill, tabStyle.Role, tabStyle.Selected = ui.Flex(1), render.Color{}, ui.RoleNone, false
	}
	c.Column(v, tabStyle, func(c *ui.Ctx) {
		c.Box("edge", ui.Style{W: ui.Flex(1), H: ui.Px(2), Fill: edge}, nil)
		if head {
			c.Row("label", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Insets{L: 14, R: 6}, Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				if led {
					c.Box("lead", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 1, Fill: lead}, nil)
				}
				if v.Project != "" {
					c.Text("p", v.Project, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11.5, Weight: typeset.SemiBold}, Color: th.C(tTabFgOn)})
				}
				c.Text("t", strings.ToUpper(name), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold, Tracking: .12},
					Color: th.C(tTabFg)})
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				closed = widget.IconButton(c, "x", "✕", widget.IconButtonOpts{Label: "Close " + title, Help: HelpTab, Size: closeSide})
			})
			return
		}
		c.Row("label", ui.Style{H: ui.Flex(1), Pad: ui.Insets{L: 14, R: 6}, Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
			if led {
				c.Box("lead", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 1, Fill: lead}, nil)
			}
			c.Row("t", ui.Style{W: ui.Size{Kind: ui.Fit}.Clamp(0, tabMaxW)}, func(c *ui.Ctx) {
				c.Text("t", title, ui.TextStyle{Font: tabFont, Color: fg})
			})
			if mark != "" {
				c.Text("mark", mark, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.SemiBold}, Color: markTone})
			}
			// The ✕ has its own room, so it never covers the title and the tab doesn't change width when it shows:
			// always on the tab in front, under the pointer on the rest.
			if (on || in.Inside) && st.drag == nil {
				closed = widget.IconButton(c, "x", "✕", widget.IconButtonOpts{Label: "Close " + title, Help: HelpTab, Size: closeSide})
			} else {
				c.Box("x", ui.Style{W: ui.Px(closeSide), H: ui.Px(closeSide)}, nil)
			}
		})
	})
	if closed {
		st.edits = append(st.edits, func() { r.Close(v) })
		return false
	}
	return changed
}

// target is where a drop at p would land, and how to show it.
func target(c *ui.Ctx, r *layout.Root, st *viewState, p render.Point) spot {
	size := c.Size()
	dock := func(e layout.Edge, rc render.Rect) spot {
		s := spot{ok: true, target: layout.Target{Dock: e}, lit: rc, area: rc, label: "Dock " + string(e)}
		if d := r.Docks[e]; d.Open && len(d.Leaf.Tabs) > 0 {
			if t, ok := onStrip(c, st, d.Leaf, rc, p, s.target); ok {
				return t
			}
		}
		// An open dock with tabs takes the view as one more tab: show it as one.
		if d := r.Docks[e]; d.Open && len(d.Leaf.Tabs) > 0 && (st.drag == nil || !slices.Contains(d.Leaf.Tabs, st.drag.view)) {
			if t, ok := tabSpot(c, st, d.Leaf, rc); ok {
				t.target, t.label = s.target, "Tab with "+tabName(st, d.Leaf.Tabs[d.Leaf.Active])
				return t
			}
		}
		return s
	}
	switch {
	case p.X < edgeBand:
		return dock(layout.Left, render.XYWH(0, 0, r.Docks[layout.Left].Size, size.Y))
	case p.X > size.X-edgeBand:
		return dock(layout.Right, render.XYWH(size.X-r.Docks[layout.Right].Size, 0, r.Docks[layout.Right].Size, size.Y))
	case p.Y > size.Y-edgeBand:
		return dock(layout.Bottom, render.XYWH(0, size.Y-r.Docks[layout.Bottom].Size, size.X, r.Docks[layout.Bottom].Size))
	}
	for e, id := range st.docks {
		if rc, ok := c.Rect(id); ok && in(p, rc) {
			return dock(e, rc)
		}
	}
	float := func() spot {
		f := render.XYWH(p.X-200, p.Y-15, 640, 400)
		return spot{ok: true, target: layout.Target{Float: &f}, lit: f, area: f, label: "Float"}
	}
	// joins is a drop on l's tab strip: one more tab there.
	joins := func(l *layout.Node, rc render.Rect) (spot, bool) {
		if len(l.Tabs) == 0 {
			return spot{}, false
		}
		b := l.Tabs[l.Active]
		return onStrip(c, st, l, rc, p, layout.Target{Beside: &b, Zone: layout.ZoneCenter})
	}
	// A float or the drop-down sits over main.
	for l, id := range st.leaves {
		if rc, ok := c.Rect(id); ok && in(p, rc) && !r.InMain(l) {
			if s, ok := joins(l, rc); ok {
				return s
			}
			return float()
		}
	}
	for l, id := range st.leaves {
		rc, ok := c.Rect(id)
		if !ok || !in(p, rc) {
			continue
		}
		if s, ok := joins(l, rc); ok {
			return s
		}
		var beside *layout.ViewRef
		if len(l.Tabs) > 0 {
			b := l.Tabs[l.Active]
			beside = &b
		}
		split := func(z layout.Zone, label string, lit render.Rect) spot {
			return spot{ok: true, target: layout.Target{Beside: beside, Zone: z}, lit: lit, area: lit, label: label}
		}
		fx, fy := (p.X-rc.Min.X)/rc.Dx(), (p.Y-rc.Min.Y)/rc.Dy()
		w, h := rc.Dx(), rc.Dy()
		switch {
		case fx < .25:
			return split(layout.ZoneLeft, "Split left", render.XYWH(rc.Min.X, rc.Min.Y, w/2, h))
		case fx > .75:
			return split(layout.ZoneRight, "Split right", render.XYWH(rc.Min.X+w/2, rc.Min.Y, w/2, h))
		case fy < .25:
			return split(layout.ZoneTop, "Split above", render.XYWH(rc.Min.X, rc.Min.Y, w, h/2))
		case fy > .75:
			return split(layout.ZoneBottom, "Split below", render.XYWH(rc.Min.X, rc.Min.Y+h/2, w, h/2))
		}
		s := split(layout.ZoneCenter, "Tab", rc)
		if beside != nil {
			s.label = "Tab with " + tabName(st, *beside)
		}
		if t, ok := tabSpot(c, st, l, rc); ok {
			t.target, t.label = s.target, s.label
			return t
		}
		return s
	}
	return float()
}

// onStrip is a drop at p on l's tab strip, l drawn at rc: it joins l's tabs as t, in the slot under p. On the
// dragged view's own strip it moves its tab there.
func onStrip(c *ui.Ctx, st *viewState, l *layout.Node, rc render.Rect, p render.Point, t layout.Target) (spot, bool) {
	row, ok := st.strips[l]
	if !ok || row.head > 0 || len(row.tabs) == 0 || st.drag == nil {
		return spot{}, false
	}
	strip, ok := c.Rect(row.id)
	if !ok || !in(p, strip) {
		return spot{}, false
	}
	i, x := len(row.tabs), strip.Min.X
	for j, id := range row.tabs {
		tr, ok := c.Rect(id)
		if !ok {
			continue
		}
		if p.X < tr.Min.X+tr.Dx()/2 {
			i, x = j, tr.Min.X
			break
		}
		x = tr.Max.X
	}
	t.Index = &i
	x = min(max(x, strip.Min.X+4), strip.Max.X-4) // clear of the strip's outline
	caret := render.XYWH(x-1, strip.Min.Y+4, 2, strip.Dy()-8)
	body := render.Rect{Min: render.Point{X: rc.Min.X, Y: strip.Max.Y + 1}, Max: rc.Max}
	if slices.Contains(l.Tabs, st.drag.view) {
		return spot{ok: true, target: t, lit: strip, caret: caret, area: body, label: "Move tab"}, true
	}
	s, ok := tabSpot(c, st, l, rc)
	if !ok {
		return spot{}, false
	}
	if i < len(row.tabs) {
		s.slot, s.caret = render.Rect{}, caret
	}
	s.target, s.label = t, "Tab with "+tabName(st, l.Tabs[l.Active])
	return s, true
}

// tabSpot shows a drop joining l's tabs, l drawn at rc: its strip outlined with the new tab's slot on the end, and
// the body under it lit where the tab's content would show.
func tabSpot(c *ui.Ctx, st *viewState, l *layout.Node, rc render.Rect) (spot, bool) {
	strip, slot, ok := tabSlot(c, st, l)
	if !ok {
		return spot{}, false
	}
	body := render.Rect{Min: render.Point{X: rc.Min.X, Y: strip.Max.Y + 1}, Max: rc.Max}
	return spot{ok: true, lit: strip, slot: slot, body: body, area: body}, true
}

// tabSlot is l's tab strip and the slot on the end of it a new tab would take.
func tabSlot(c *ui.Ctx, st *viewState, l *layout.Node) (strip, slot render.Rect, ok bool) {
	row, ok := st.strips[l]
	if !ok {
		return strip, slot, false
	}
	strip, ok = c.Rect(row.id)
	if !ok {
		return strip, slot, false
	}
	x := strip.Min.X
	if last, ok := c.Rect(row.last); ok && len(l.Tabs) > 0 {
		end := last.Max.X
		if row.head > 0 {
			end = last.Min.X + row.head
		}
		x = min(end, strip.Max.X-slotW)
	}
	return strip, render.XYWH(max(x, strip.Min.X), strip.Min.Y, min(slotW, strip.Dx()), strip.Dy()), true
}

// slotW is the width of the slot a tab dropped on a strip would take.
const slotW = 140

// tabMaxW is the widest a tab's title runs before it's cut with "…".
const tabMaxW = 240

var tabFont = typeset.FontSpec{Family: typeset.UI, Size: 12}

func tabName(st *viewState, v layout.ViewRef) string {
	if st.title != nil {
		if t := st.title(v); t != "" {
			return t
		}
	}
	if v.Title != "" {
		return v.Title
	}
	return layout.ViewRef{ID: v.ID, Instance: v.Instance}.String()
}

func in(p render.Point, r render.Rect) bool {
	return p.X >= r.Min.X && p.X < r.Max.X && p.Y >= r.Min.Y && p.Y < r.Max.Y
}

// indicator lights up a drag's drop target, gliding between targets on springs, and names what a drop there does. A
// float is only outlined, dashed: it snaps to nothing.
func indicator(c *ui.Ctx, st *viewState, d *drag) {
	th := c.Theme()
	s := d.spot
	x := c.Anim("x", s.lit.Min.X)
	y := c.Anim("y", s.lit.Min.Y)
	w := c.Anim("w", s.lit.Dx())
	h := c.Anim("h", s.lit.Dy())
	at := render.Point{X: x, Y: y}
	edge := th.C(tDropEdge)
	if s.target.Float != nil {
		c.Box("drop", ui.Style{W: ui.Px(w), H: ui.Px(h), Z: ui.LayerDrag, Float: &ui.Float{Point: &at}, Help: HelpDrop}, func(c *ui.Ctx) {
			c.Paint("dash", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
				r = render.Rect{Min: render.Point{X: r.Min.X + 1, Y: r.Min.Y + 1}, Max: render.Point{X: r.Max.X - 1, Y: r.Max.Y - 1}}
				corners := []render.Point{r.Min, {X: r.Max.X, Y: r.Min.Y}, r.Max, {X: r.Min.X, Y: r.Max.Y}, r.Min}
				f.Path(render.Dashed(corners, 6, 4), render.Style{Stroke: edge, Width: 1.5})
			})
		})
	} else {
		// The glow sits behind the target, as a box-shadow does; the target's fill covers its inside.
		c.Box("glow", ui.Style{W: ui.Px(w), H: ui.Px(h), Z: ui.LayerDrag, Float: &ui.Float{Point: &at}}, func(c *ui.Ctx) {
			c.Paint("fx", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
				f.Effect(render.Glow, r, 12, theme.WithAlpha(edge, 0.35*th.Effect("glow")))
			})
		})
		// A lit tab strip is only outlined, so its tabs still show beside the slot.
		fill := th.C(tDrop)
		if s.slot != (render.Rect{}) || s.caret != (render.Rect{}) {
			fill = render.Color{}
		}
		c.Box("drop", ui.Style{W: ui.Px(w), H: ui.Px(h), Z: ui.LayerDrag, Fill: fill, Stroke: edge, Border: 1.5, Radius: 6,
			Float: &ui.Float{Point: &at}, Help: HelpDrop}, nil)
	}
	if k := s.caret; k != (render.Rect{}) {
		c.Box("caret", ui.Style{W: ui.Px(k.Dx()), H: ui.Px(k.Dy()), Z: ui.LayerDrag + 1, Fill: edge, Radius: 1, Float: &ui.Float{Point: &k.Min}}, nil)
	}
	if b := s.body; b != (render.Rect{}) {
		c.Box("body", ui.Style{W: ui.Px(b.Dx()), H: ui.Px(b.Dy()), Z: ui.LayerDrag, Fill: th.C(tDrop), Stroke: theme.WithAlpha(edge, .5),
			Border: 1, Radius: 6, Float: &ui.Float{Point: &b.Min}}, nil)
	}
	font := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: th.C(tTabFgOn)}
	name := strings.TrimSpace(tabName(st, d.view))
	if s.slot != (render.Rect{}) {
		c.Row("slot", ui.Style{W: ui.Px(s.slot.Dx()), H: ui.Px(s.slot.Dy()), Pad: ui.Insets{L: 14, R: 6}, Z: ui.LayerDrag + 1, Fill: th.C(tTabActive),
			Stroke: edge, Border: 1, Radius: 4, Cross: ui.Center, Float: &ui.Float{Point: &s.slot.Min}}, func(c *ui.Ctx) {
			c.Text("t", name, font)
		})
	}
	c.Box("label", ui.Style{W: ui.Px(s.area.Dx()), H: ui.Px(s.area.Dy()), Z: ui.LayerDrag + 1, Main: ui.Center, Cross: ui.Center,
		Float: &ui.Float{Point: &s.area.Min}}, func(c *ui.Ctx) {
		c.Row("pill", ui.Style{Pad: ui.PadXY(12, 6), Fill: th.C(tTabActive), Stroke: edge, Border: 1, Radius: 6}, func(c *ui.Ctx) {
			c.Text("t", s.label, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12, Weight: typeset.SemiBold}, Color: th.C(tTabFgOn)})
		})
	})
	ghost := render.Point{X: d.at.X + 10, Y: d.at.Y + 10}
	c.Row("ghost", ui.Style{Pad: ui.PadXY(10, 4), Z: ui.LayerDrag + 1, Fill: th.C(tTabActive), Stroke: edge, Border: 1, Radius: 4,
		Float: &ui.Float{Point: &ghost}}, func(c *ui.Ctx) {
		c.Text("t", name, font)
	})
}

// Panes tracks where View drew each leaf, for FocusDir. Title, when set, names a view's tab; a view it gives no
// name keeps its own.
type Panes struct {
	leaves map[*layout.Node]ui.ID
	tabs   map[ui.ID]layout.ViewRef
	Title  func(v layout.ViewRef) string
	// Mark, when set, is a small marker after a view's tab title, in its colour: a file's git status letter.
	Mark func(v layout.ViewRef) (string, render.Color)
	// Lead, when set and ok, is the colour of a small square leading a view's tab: its project's.
	Lead func(v layout.ViewRef) (render.Color, bool)
	// Tip, when set, is a tab's tooltip; "" for none.
	Tip   func(v layout.ViewRef) string
	Empty func(c *ui.Ctx) // draws the main area while it holds no views, unless a pane is being dragged there
}

// Rects is where each leaf was drawn in the latest frame.
func (p *Panes) Rects(u *ui.UI) map[*layout.Node]render.Rect {
	out := map[*layout.Node]render.Rect{}
	for l, id := range p.leaves {
		if rc, ok := u.Rect(id); ok {
			out[l] = rc
		}
	}
	return out
}

// TabAt is the view whose tab holds node id in the latest frame: a right-click on it, say.
func (p *Panes) TabAt(u *ui.UI, id ui.ID) (layout.ViewRef, bool) {
	for tab, v := range p.tabs {
		if u.Within(id, tab) {
			return v, true
		}
	}
	return layout.ViewRef{}, false
}

// RectsIn is Rects from inside a frame being built: last frame's rects.
func (p *Panes) RectsIn(c *ui.Ctx) map[*layout.Node]render.Rect {
	out := map[*layout.Node]render.Rect{}
	for l, id := range p.leaves {
		if rc, ok := c.Rect(id); ok {
			out[l] = rc
		}
	}
	return out
}

var dockNames = map[layout.Edge]string{layout.Left: "Left dock", layout.Right: "Right dock", layout.Bottom: "Bottom dock"}

func title(v layout.ViewRef) string {
	t := v.Title
	if t == "" {
		t = layout.ViewRef{ID: v.ID, Instance: v.Instance}.String()
	}
	if v.Project != "" {
		t = v.Project + " · " + t
	}
	return t
}
