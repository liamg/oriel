package widget

import (
	"strings"

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
			for i, l := range shown {
				on := i == *active
				fill, fg := render.Color{}, col(c, tSegFg)
				if on {
					fill, fg = col(c, tSegOnBg), col(c, tSegOnFg)
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

// PointerIn reports whether the pointer is in the box being built, or on something clickable inside it: for what
// shows only on hover, such as a row's remove button.
func PointerIn(c *ui.Ctx) bool { return c.Interaction(c.ID()).Inside }

type IconButtonOpts struct {
	Label   string // its accessible name, and its tooltip
	Help    string
	Size    float32                          // its side; 20 if 0
	Tooltip string                           // when it should say more than Label
	Icon    func(c *ui.Ctx, fg render.Color) // drawn in place of the glyph
	On      bool                             // lit, as while what it opens is showing
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
	if o.On {
		fg, fill = col(c, tSegOnFg), col(c, tSegOnBg)
	}
	return Clickable(c, key, ui.Style{W: ui.Px(side), H: ui.Px(side), Radius: 5, Main: ui.Center, Cross: ui.Center, Fill: fill, Label: o.Label,
		Tooltip: tip, Selected: o.On, Help: o.Help}, func(c *ui.Ctx) {
		if o.Icon != nil {
			o.Icon(c, fg)
			return
		}
		c.Text("g", glyph, monoText(side*.55, typeset.Medium, fg, 0))
	}).Clicked
}
