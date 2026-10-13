package widget

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/ui"
)

// Clickable is a box that takes clicks, built with body: interactive, with the pointer cursor and a button's role
// unless st says otherwise, so it gets the toolkit's hover and press. Focusable, Enter or Space click it too. It
// returns its interaction this frame.
func Clickable(c *ui.Ctx, key any, st ui.Style, body func(c *ui.Ctx)) ui.Interaction {
	id := c.Next(key)
	in := c.Interaction(id)
	if st.Focusable && pressedKey(in.Keys) {
		in.Clicked = true
	}
	passOn(c, id, in.Keys, activates)
	if st.Disabled {
		in.Clicked = false
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
}

// Segmented is a strip of mono buttons, *active lit; a click picks one. It reports a change.
func Segmented(c *ui.Ctx, key any, labels []string, active *int, o SegmentedOpts) bool {
	changed := false
	c.Row(key, ui.Style{H: ui.Px(24), Stroke: col(c, tSegBorder), Border: 1, Radius: 5, Clip: true, Role: ui.RoleTabList, Label: o.Label},
		func(c *ui.Ctx) {
			for i, l := range labels {
				on := i == *active
				fill, fg := render.Color{}, col(c, tSegFg)
				if on {
					fill, fg = col(c, tSegOnBg), col(c, tSegOnFg)
				}
				in := Clickable(c, i, ui.Style{Dir: ui.Row, H: ui.Flex(1), Pad: ui.PadXY(10, 0), Cross: ui.Center, Fill: fill, Help: o.Help,
					Role: ui.RoleTab, Label: l, Selected: on}, func(c *ui.Ctx) {
					c.Text("l", l, monoText(9.5, typeset.SemiBold, fg, .1))
				})
				if in.Clicked && !on {
					*active, changed = i, true
				}
			}
		})
	return changed
}

// PointerIn reports whether the pointer is in the box being built, or on something clickable inside it: for what
// shows only on hover, such as a row's remove button.
func PointerIn(c *ui.Ctx) bool { return c.Interaction(c.ID()).Inside }

type IconButtonOpts struct {
	Label   string // its accessible name, and its tooltip
	Help    string
	Size    float32 // its side; 20 if 0
	Tooltip string  // when it should say more than Label
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
	return Clickable(c, key, ui.Style{W: ui.Px(side), H: ui.Px(side), Radius: 5, Main: ui.Center, Cross: ui.Center, Label: o.Label, Tooltip: tip,
		Help: o.Help}, func(c *ui.Ctx) {
		c.Text("g", glyph, monoText(side*.55, typeset.Medium, col(c, tIconButtonFg), 0))
	}).Clicked
}
