package widget

import (
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/ui"
)

// Clickable is a box that takes clicks, built with body: interactive, with the pointer cursor and a button's role
// unless st says otherwise, so it gets the toolkit's hover and press. Focusable, Enter or Space click it too. It
// returns its interaction this frame, with DoubleClicked on the second of two quick clicks.
func Clickable(c *ui.Ctx, key any, st ui.Style, body func(c *ui.Ctx)) ui.Interaction {
	id := c.Next(key)
	in := c.Interaction(id)
	if st.Focusable && pressedKey(in.Keys) {
		in.Clicked = true
	}
	passOn(c, id, in.Keys, activates)
	if st.Disabled {
		in.Clicked, in.DoubleClicked = false, false
	}
	st.Interactive = true
	if st.Cursor == render.CursorDefault {
		st.Cursor = render.CursorPointer
	}
	if st.Focusable && st.KeyMode == "" {
		st.KeyMode = "button"
	}
	if st.Role == ui.RoleNone {
		st.Role = ui.RoleButton
	}
	c.Box(key, st, body)
	return in
}

type SegmentedOpts struct {
	Help  string
	Label string // the strip's accessible name
	// Short are shorter labels, one for each: with them the strip fills the width it's given and uses them when the
	// full labels don't fit.
	Short []string
	// Badges are small chips after the labels, one for each ("" for none): a plan's tier.
	Badges []string
}

const segPad = 10

// Segmented is a strip of mono buttons, *active lit; a click picks one. It reports a change.
func Segmented(c *ui.Ctx, key any, labels []string, active *int, o SegmentedOpts) bool {
	changed := false
	style := monoText(9.5, typeset.SemiBold, col(c, tSegFg), .1)
	strip := ui.Style{H: ui.Px(24), Stroke: col(c, tSegBorder), Border: 1, Radius: 5, Clip: true, Role: ui.RoleTabList, Label: o.Label}
	seg := ui.Size{}
	shown := labels
	if len(o.Short) == len(labels) {
		strip.W, seg = ui.Flex(1), ui.Flex(1)
		var natural float32
		for _, l := range labels {
			natural += c.Shaper().Shape(l, style.Font).Advance + 2*segPad
		}
		if r, ok := c.Rect(c.Next(key)); ok && r.Dx() < natural {
			shown = o.Short
		}
	}
	c.Row(key, strip,
		func(c *ui.Ctx) {
			placed := glideMark(c, "pill", *active, len(shown), col(c, tSegOnBg), 0, ui.LayerUnder)
			for i, l := range shown {
				on := i == *active
				fill, fg := render.Color{}, col(c, tSegFg)
				if on {
					fg = col(c, tSegOnFg)
					if !placed {
						fill = col(c, tSegOnBg)
					}
				}
				badge := ""
				if i < len(o.Badges) {
					badge = o.Badges[i]
				}
				name := labels[i]
				if badge != "" {
					name += ", " + badge
				}
				in := Clickable(c, i, ui.Style{Dir: ui.Row, W: seg, H: ui.Flex(1), Pad: ui.PadXY(segPad, 0), Gap: 6, Main: ui.Center, Cross: ui.Center,
					Fill: fill, Help: o.Help, Role: ui.RoleTab, Label: name, Selected: on}, func(c *ui.Ctx) {
					st := style
					st.Color = fg
					c.Text("l", l, st)
					if badge != "" {
						c.Row("badge", ui.Style{H: ui.Px(14), Pad: ui.PadXY(4, 0), Cross: ui.Center, Radius: 3, Stroke: alpha(fg, .45), Border: 1}, func(c *ui.Ctx) {
							c.Text("t", strings.ToUpper(badge), monoText(8.5, typeset.SemiBold, fg, .06))
						})
					}
				})
				if in.Clicked && !on {
					*active, changed = i, true
				}
			}
		})
	return changed
}

// glideMark marks item active of the n items keyed 0..n-1 in the box being built, gliding between them as active
// changes: a selected segment's fill, behind them on LayerUnder, or with h an underline that tall along their foot.
// It places the mark by last frame's layout and reports whether it could.
func glideMark(c *ui.Ctx, key any, active, n int, fill render.Color, h float32, z int) bool {
	if active < 0 || active >= n {
		return false
	}
	at := c.Ease(markKey{key}, float32(active))
	lo := max(0, min(n-1, int(at)))
	hi := min(n-1, lo+1)
	box, ok0 := c.Rect(c.ID())
	a, ok1 := c.Rect(ui.Child(c.ID(), lo))
	b, ok2 := c.Rect(ui.Child(c.ID(), hi))
	if !ok0 || !ok1 || !ok2 || a.Dx() <= 0 || b.Dx() <= 0 {
		return false
	}
	// Placed against the box as it's laid out now, by where the items sat in it, so it keeps up with a scroll.
	f := at - float32(lo)
	x0, x1 := a.Min.X+(b.Min.X-a.Min.X)*f, a.Max.X+(b.Max.X-a.Max.X)*f
	off := render.Point{X: x0 - box.Min.X, Y: a.Min.Y - box.Min.Y}
	height := a.Dy()
	if h > 0 {
		off.Y, height = a.Max.Y-h-box.Min.Y, h
	}
	c.Box(key, ui.Style{W: ui.Px(x1 - x0), H: ui.Px(height), Fill: fill, Z: z, Float: &ui.Float{Offset: off}}, nil)
	return true
}

type markKey struct{ key any }

// PointerIn reports whether the pointer is in the box being built, or on something clickable inside it: for what
// shows only on hover, such as a row's remove button.
func PointerIn(c *ui.Ctx) bool { return c.Interaction(c.ID()).Inside }

type IconButtonOpts struct {
	Label    string // its accessible name, and its tooltip
	Help     string
	Size     float32                          // its side; 20 if 0
	Tooltip  string                           // when it should say more than Label
	Icon     func(c *ui.Ctx, fg render.Color) // drawn in place of the glyph
	On       bool                             // lit, as while what it opens is showing
	Disabled bool
}

// IconButton is a small square button showing a glyph, such as ✕. It reports a click.
func IconButton(c *ui.Ctx, key any, glyph string, o IconButtonOpts) bool {
	side := o.Size
	if side == 0 {
		side = 20
	}
	tip := o.Tooltip
	if tip == "" {
		tip = o.Label
	}
	fg, fill := col(c, tIconButtonFg), render.Color{}
	switch {
	case o.Disabled:
		fg = col(c, tDisabledFg)
	case o.On:
		fg, fill = col(c, tSegOnFg), col(c, tSegOnBg)
	}
	return Clickable(c, key, ui.Style{W: ui.Px(side), H: ui.Px(side), Radius: 5, Main: ui.Center, Cross: ui.Center, Fill: fill, Label: o.Label,
		Tooltip: tip, Selected: o.On, Disabled: o.Disabled, Help: o.Help, Sink: true}, func(c *ui.Ctx) {
		if o.Icon != nil {
			o.Icon(c, fg)
			return
		}
		c.Text("g", glyph, monoText(side*.55, typeset.Medium, fg, 0))
	}).Clicked
}
