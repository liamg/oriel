package ui

import (
	"math"
	"slices"

	"github.com/liamg/oriel/render"
)

// DragData is what a drag carries: a kind ("file", "text", or a plugin's own, "linear.issue"), its data (paths for
// "file"), and the label its ghost shows.
type DragData struct {
	Kind  string
	Data  any
	Label string
}

// DragKeyMode is the key mode of a drag source or target that doesn't name its own: the window maps Space (pick up
// or drop), Esc and Tab in it.
const DragKeyMode = "dnd"

// dragSlop is how far the pointer moves with the button down before a press becomes a drag.
const dragSlop = 4

// dragging is a drag under way: from a press on its source (moving once past dragSlop), or picked up from the
// keyboard, and the target under it that takes its kind.
type dragging struct {
	data     DragData
	source   ID
	from, at render.Point
	moving   bool
	keyboard bool
	target   ID
}

// takes is whether id takes a drag of kind.
func (u *UI) takes(id ID, kind string) bool { return slices.Contains(u.flags[id].drop, kind) }

// dropTarget is the topmost node at p that takes kind.
func (u *UI) dropTarget(p render.Point, kind string) ID {
	for _, id := range u.Hit(p) {
		if u.takes(id, kind) {
			return id
		}
	}
	return 0
}

func (u *UI) dragPointer(ev render.PointerEvent) {
	in := &u.in
	switch {
	case ev.Kind == render.PointerPress && ev.Buttons&render.ButtonPrimary != 0:
		in.dnd = nil
		for id := in.hot; id != 0; id = u.parents[id] {
			if d := u.flags[id].drag; d != nil {
				in.dnd = &dragging{data: *d, source: id, from: ev.Pos, at: ev.Pos}
				break
			}
		}
	case in.dnd != nil && !in.dnd.keyboard:
		d := in.dnd
		d.at = ev.Pos
		if !d.moving && math.Hypot(float64(d.at.X-d.from.X), float64(d.at.Y-d.from.Y)) > dragSlop {
			d.moving = true
		}
		if d.moving {
			d.target = u.dropTarget(ev.Pos, d.data.Kind)
		}
	}
}

// dragRelease ends a pointer drag: on a target that takes it, it's dropped there. It reports whether a drag
// ended, so the release isn't also a click.
func (u *UI) dragRelease() bool {
	in := &u.in
	d := in.dnd
	if d == nil || d.keyboard {
		return false
	}
	in.dnd = nil
	if !d.moving {
		return false
	}
	if d.target != 0 {
		u.drop(d.target, d.data)
	}
	return true
}

func (u *UI) drop(id ID, data DragData) {
	if u.in.dropped == nil {
		u.in.dropped = map[ID]DragData{}
	}
	u.in.dropped[id] = data
}

// DragKey is Space on a drag source or target: it picks up what the focused node carries, or drops what's held on
// it when it takes the kind. It reports whether it did either.
func (u *UI) DragKey() bool {
	in := &u.in
	if in.dnd == nil {
		if d := u.flags[in.focus].drag; d != nil {
			in.dnd = &dragging{data: *d, source: in.focus, moving: true, keyboard: true}
			return true
		}
		return false
	}
	if u.takes(in.focus, in.dnd.data.Kind) {
		u.drop(in.focus, in.dnd.data)
		in.dnd = nil
		return true
	}
	return false
}

// CancelDrag drops a drag held from the keyboard nowhere.
func (u *UI) CancelDrag() { u.in.dnd = nil }

// Dragging is whether a drag is under way.
func (u *UI) Dragging() bool { return u.in.dnd != nil && u.in.dnd.moving }

// DropFiles hands files dropped on the window to the node at p that takes "file". It reports whether one did.
func (u *UI) DropFiles(p render.Point, paths []string) bool {
	id := u.dropTarget(p, "file")
	if id == 0 {
		return false
	}
	u.drop(id, DragData{Kind: "file", Data: paths, Label: "files"})
	return true
}

type dragGhostKey struct{}
type dragGlowKey struct{}

// dragShown draws a moving drag: its target lit, as the layout's drop targets are, and a ghost of its label by the
// pointer (by its source, held from the keyboard).
func (u *UI) dragShown() {
	in := &u.in
	d := in.dnd
	if d == nil || !d.moving {
		return
	}
	if d.keyboard {
		d.target = 0
		if u.takes(in.focus, d.data.Kind) {
			d.target = in.focus
		}
	}
	c := &Ctx{u: u, e: u.root}
	ring := u.opts.FocusRing
	if r, ok := u.Rect(d.target); ok && d.target != 0 {
		const out = 3 // the glow sits just outside the target
		at := render.Point{X: r.Min.X - out, Y: r.Min.Y - out}
		glow := ring
		glow.A = 0x24
		c.Box(dragGlowKey{}, Style{W: Px(r.Dx() + 2*out), H: Px(r.Dy() + 2*out), Fill: glow, Stroke: ring, Border: 2, Radius: 6, Z: LayerDrag,
			Float: &Float{Point: &at}}, nil)
	}
	at := d.at
	if d.keyboard {
		if r, ok := u.Rect(d.source); ok {
			at = render.Point{X: r.Max.X, Y: r.Min.Y}
		}
	}
	label := d.data.Label
	if label == "" {
		label = d.data.Kind
	}
	c.Box(dragGhostKey{}, Style{Pad: PadXY(8, 4), Fill: u.opts.TooltipFill, Stroke: ring, Border: 1, Radius: 4, Z: LayerDrag + 1,
		Float: &Float{Point: &at, Offset: render.Point{X: 12, Y: 12}}}, func(c *Ctx) {
		c.Text(nil, label, u.opts.Tooltip)
	})
}
