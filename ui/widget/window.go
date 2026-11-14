package widget

import (
	"cmp"
	"fmt"
	"image/color"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tWindowBg    = theme.Declare("window.bg", "alpha(bg.raised, 97%)")
	tWindowEdge  = theme.Declare("window.edge", "alpha(accent, 35%)")
	tWindowGlow  = theme.Declare("window.edge.glow", "accent")
	tWindowHead  = theme.DeclareOn("window.head", "alpha(fg, 3%)", "bg.raised")
	tWindowTitle = theme.Declare("window.title", "fg")
	tWindowGrip  = theme.Declare("window.grip", "alpha(accent, 55%)")
	tBadgeBg     = theme.Declare("badge.bg", "amber")
	tBadgeFg     = theme.Declare("badge.fg", "ink")
)

// WindowState is where a floating window is, kept by its owner. A zero Rect opens it at WindowOpts.Default.
type WindowState struct {
	Rect render.Rect
	grab *windowGrab
}

// Dragging says whether its title bar or an edge is being dragged.
func (s *WindowState) Dragging() bool { return s.grab != nil }

type windowGrab struct {
	part  windowPart
	from  render.Point
	rect0 render.Rect
}

type windowPart int

const (
	partHead windowPart = iota
	partLeft
	partRight
	partBottom
	partBottomLeft
	partBottomRight
)

type WindowOpts struct {
	Title   string
	Lead    func(c *ui.Ctx) // drawn before the title: an avatar, an icon
	Head    func(c *ui.Ctx) // drawn after the title, before the buttons: chips, a status
	Default render.Rect     // where it opens first
	Min     render.Point    // the smallest it's resized to; 240×160 if zero
	Z       int             // its layer; ui.LayerWindow if zero
	// Minimise and Close show those buttons.
	Minimise, Close bool
	Label, Help     string
	KeyMode         string // its keys, Escape among them, while focus is in it
}

type WindowResult struct {
	ID                ui.ID
	Minimised, Closed bool
	Moved             bool // dragged or resized this frame
}

const (
	windowHeadH  = 32
	windowEdge   = 6
	windowCorner = 14
)

// Window is a floating window over the IDE: its title bar drags it, its left, right and bottom edges and bottom
// corners resize it, and it stays in the viewport. It doesn't trap focus: what's under it still takes clicks and
// keys.
func Window(c *ui.Ctx, key any, st *WindowState, o WindowOpts, body func(c *ui.Ctx)) WindowResult {
	view := c.Size()
	if o.Min == (render.Point{}) {
		o.Min = render.Point{X: 240, Y: 160}
	}
	if o.Z == 0 {
		o.Z = ui.LayerWindow
	}
	if st.Rect.Dx() <= 0 || st.Rect.Dy() <= 0 {
		st.Rect = o.Default
	}
	st.Rect = fitWindow(st.Rect, o.Min, view)
	id := c.Next(key)
	res := WindowResult{ID: id}
	rect := st.Rect
	at := rect.Min
	edge, glow := col(c, tWindowEdge), col(c, tWindowGlow)
	c.Column(key, ui.Style{W: ui.Px(rect.Dx()), H: ui.Px(rect.Dy()), Fill: col(c, tWindowBg), Stroke: edge, Border: 1, Radius: 10,
		Clip: true, Z: o.Z, Float: &ui.Float{Point: &at}, Interactive: true, NoHover: true, Focusable: true, KeyMode: o.KeyMode, Selectable: true,
		Role: ui.RoleDialog, Label: cmp.Or(o.Label, o.Title), Help: o.Help,
		Enter: &ui.Transition{Shift: render.Point{Y: 16}, Fade: true}, Exit: &ui.Transition{Shift: render.Point{Y: 16}, Fade: true}},
		func(c *ui.Ctx) {
			c.Box("glow", ui.Style{Float: &ui.Float{Fill: true}, W: ui.Flex(1), H: ui.Flex(1), Z: ui.LayerUnder}, func(c *ui.Ctx) {
				c.Paint("line", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
					mid := r.Min.X + r.Dx()/2
					w := min(160, r.Dx()/3)
					line := render.Rect{Min: render.Point{X: mid - w, Y: r.Min.Y}, Max: render.Point{X: mid + w, Y: r.Min.Y + 1}}
					f.Effect(render.Glow, line, 8, theme.WithAlpha(glow, .45))
					f.Rect(line, render.Style{Fill: glow})
				})
			})
			if windowPartDrag(c, st, partHead, view, o.Min, func(c *ui.Ctx, key any, s ui.Style) {
				s.W, s.H = ui.Flex(1), ui.Px(windowHeadH)
				s.Dir, s.Pad, s.Gap, s.Cross, s.Fill = ui.Row, ui.PadXY(10, 0), 8, ui.Center, col(c, tWindowHead)
				s.Label = cmp.Or(o.Title, o.Label)
				c.Box(key, s, func(c *ui.Ctx) {
					if o.Lead != nil {
						o.Lead(c)
					}
					if o.Title != "" {
						c.Text("title", o.Title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11.5, Weight: typeset.Bold,
							Tracking: .2}, Color: col(c, tWindowTitle)})
					}
					c.Row("head", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
						if o.Head != nil {
							o.Head(c)
						}
					})
					if o.Minimise && IconButton(c, "min", "–", IconButtonOpts{Label: "Minimise", Help: o.Help, Size: 20}) {
						res.Minimised = true
					}
					if o.Close && IconButton(c, "close", "✕", IconButtonOpts{Label: "Close", Help: o.Help, Size: 20}) {
						res.Closed = true
					}
				})
			}) {
				res.Moved = true
			}
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: col(c, theme.Line)}, nil)
			c.Column("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Clip: true}, body)
			w, h := rect.Dx(), rect.Dy()
			grips := []struct {
				part     windowPart
				at, self render.Point
				w, h     float32
				cur      render.Cursor
			}{
				{partLeft, render.Point{}, render.Point{}, windowEdge, h - windowCorner, render.CursorResizeCol},
				{partRight, render.Point{X: 1}, render.Point{X: 1}, windowEdge, h - windowCorner, render.CursorResizeCol},
				{partBottom, render.Point{X: .5, Y: 1}, render.Point{X: .5, Y: 1}, w - 2*windowCorner, windowEdge, render.CursorResizeRow},
				{partBottomLeft, render.Point{Y: 1}, render.Point{Y: 1}, windowCorner, windowCorner, render.CursorResizeNESW},
				{partBottomRight, render.Point{X: 1, Y: 1}, render.Point{X: 1, Y: 1}, windowCorner, windowCorner, render.CursorResizeNWSE},
			}
			for _, g := range grips {
				if windowPartDrag(c, st, g.part, view, o.Min, func(c *ui.Ctx, key any, s ui.Style) {
					s.W, s.H, s.Cursor, s.Z = ui.Px(g.w), ui.Px(g.h), g.cur, ui.LayerContent+1
					s.Float = &ui.Float{At: g.at, Self: g.self}
					c.Box(key, s, func(c *ui.Ctx) {
						if g.part == partBottomRight {
							c.Paint("grip", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
								var p render.Path
								for i := float32(1); i <= 2; i++ {
									p.MoveTo(render.Point{X: r.Max.X - 4 - i*4, Y: r.Max.Y - 3}).LineTo(render.Point{X: r.Max.X - 3, Y: r.Max.Y - 4 - i*4})
								}
								f.Path(&p, render.Style{Stroke: col(c, tWindowGrip), Width: 1})
							})
						}
					})
				}) {
					res.Moved = true
				}
			}
		})
	return res
}

// windowPartDrag draws one part of a window that's dragged, its title bar or a grip, with draw, and moves or resizes
// the window as it's dragged.
func windowPartDrag(c *ui.Ctx, st *WindowState, part windowPart, view, minSize render.Point, draw func(c *ui.Ctx, key any, s ui.Style)) bool {
	key := windowPartKey{part}
	in := c.Interaction(c.Next(key))
	moved := false
	switch {
	case in.Held:
		p := in.Pointer()
		if st.grab == nil || st.grab.part != part {
			from, ok := in.PressedAt()
			if !ok {
				from = p
			}
			st.grab = &windowGrab{part: part, from: from, rect0: st.Rect}
		}
		if r := fitWindow(dragWindow(st.grab, p, minSize, view), minSize, view); r != st.Rect {
			st.Rect, moved = r, true
		}
	case st.grab != nil && st.grab.part == part:
		st.grab = nil
	}
	s := ui.Style{Interactive: true, NoHover: true}
	if part == partHead {
		s.Cursor = render.CursorGrab
		if in.Held {
			s.Cursor = render.CursorGrabbing
		}
	}
	draw(c, key, s)
	return moved
}

type windowPartKey struct{ part windowPart }

// dragWindow is the window's rect with g dragged to p: a moved one keeps its size, a resized one's edges stop at the
// view's.
func dragWindow(g *windowGrab, p, minSize, view render.Point) render.Rect {
	d := render.Point{X: p.X - g.from.X, Y: p.Y - g.from.Y}
	r := g.rect0
	switch g.part {
	case partHead:
		return render.Rect{Min: render.Point{X: r.Min.X + d.X, Y: r.Min.Y + d.Y}, Max: render.Point{X: r.Max.X + d.X, Y: r.Max.Y + d.Y}}
	case partLeft, partBottomLeft:
		r.Min.X = max(0, min(r.Min.X+d.X, r.Max.X-minSize.X))
	case partRight, partBottomRight:
		r.Max.X = min(view.X, max(r.Max.X+d.X, r.Min.X+minSize.X))
	}
	if g.part == partBottom || g.part == partBottomLeft || g.part == partBottomRight {
		r.Max.Y = min(view.Y, max(r.Max.Y+d.Y, r.Min.Y+minSize.Y))
	}
	return r
}

// fitWindow keeps r in a view of size view: no smaller than minSize, no bigger than the view, all of it in it.
func fitWindow(r render.Rect, minSize, view render.Point) render.Rect {
	if view.X <= 0 || view.Y <= 0 {
		return r
	}
	w := min(max(r.Dx(), minSize.X), view.X)
	h := min(max(r.Dy(), minSize.Y), view.Y)
	x := min(max(r.Min.X, 0), view.X-w)
	y := min(max(r.Min.Y, 0), view.Y-h)
	return render.Rect{Min: render.Point{X: x, Y: y}, Max: render.Point{X: x + w, Y: y + h}}
}

type BadgeOpts struct {
	Fill, Ink color.NRGBA // badge.bg and badge.fg if zero
	Size      float32     // its text's size; 9 if zero
}

// Badge is a count in a pill, as tabs, the rail and launchers show what's unread; past 99 it says 99+. It draws
// nothing for 0 or less.
func Badge(c *ui.Ctx, key any, n int, o BadgeOpts) {
	if n <= 0 {
		return
	}
	fill, ink := o.Fill, o.Ink
	if fill == (color.NRGBA{}) {
		fill = col(c, tBadgeBg)
	}
	if ink == (color.NRGBA{}) {
		ink = col(c, tBadgeFg)
	}
	size := o.Size
	if size == 0 {
		size = 9
	}
	text := fmt.Sprint(n)
	if n > 99 {
		text = "99+"
	}
	h := size + 5
	ts := monoText(size, typeset.Bold, ink, 0)
	// A new count rolls in from below as the old one goes up and out.
	id := c.Next(key)
	roll := ui.State[badgeRoll](c, ui.Child(id, "roll"))
	if roll.text != text {
		if roll.text != "" {
			roll.old = roll.text
			c.ResetMotion(badgeKey{id}, 0)
		}
		roll.text = text
	}
	p := c.Motion(badgeKey{id}, 1)
	rolling := p < 1 && roll.old != ""
	c.Box(key, ui.Style{H: ui.Px(h), Pad: ui.PadXY(4, 0), Radius: h / 2, Fill: fill, Cross: ui.Center, Main: ui.Center, Role: ui.RoleText,
		Label: text, Clip: rolling, Enter: &ui.Transition{Fade: true, Shift: render.Point{Y: 3}}, Exit: fadeIn}, func(c *ui.Ctx) {
		if !rolling {
			c.Text("n", text, ts)
			return
		}
		c.Box("n", ui.Style{Fade: 1}, func(c *ui.Ctx) { c.Text("t", text, ts) })
		c.Column("roll", ui.Style{Z: ui.LayerUnder, Float: &ui.Float{Offset: render.Point{X: 4, Y: -h * p}}}, func(c *ui.Ctx) {
			for i, t := range []string{roll.old, text} {
				c.Row(i, ui.Style{H: ui.Px(h), Cross: ui.Center}, func(c *ui.Ctx) { c.Text("t", t, ts) })
			}
		})
	})
}

type badgeRoll struct{ text, old string }

type badgeKey struct{ id ui.ID }
