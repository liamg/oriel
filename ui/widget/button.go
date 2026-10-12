package widget

import (
	"image/color"
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/ui"
)

type ButtonOpts struct {
	Primary  bool
	Ghost    bool // no fill until hovered
	Disabled bool
	Help     string
}

// Button is a 26px push button (base.css .btn). It returns true on the frame it's clicked, or pressed with
// Enter or Space while focused.
func Button(c *ui.Ctx, key any, label string, o ButtonOpts) bool {
	id := c.Next(key)
	in := c.Interaction(id)
	bg, hover, pressed, fg, border := col(c, tButtonBg), col(c, tButtonHover), col(c, tButtonPressed), col(c, tButtonFg), col(c, tButtonBorder)
	if o.Primary {
		bg, hover, pressed, fg = col(c, tPrimaryBg), col(c, tPrimaryHover), col(c, tPrimaryPressed), col(c, tPrimaryFg)
		border = bg
	}
	switch {
	case o.Disabled:
		fg = col(c, tDisabledFg)
	case in.Pressed:
		bg = pressed
	case in.Hovered:
		bg = hover
	case o.Ghost:
		bg = color.NRGBA{}
	}
	weight := typeset.Medium
	if o.Primary {
		weight = typeset.SemiBold
	}
	c.Row(key, ui.Style{H: ui.Px(26), Pad: ui.PadXY(10, 0), Gap: 6, Cross: ui.Center, Fill: bg, Stroke: border, Border: 1, Radius: 6,
		Focusable: true, Disabled: o.Disabled, Cursor: render.CursorPointer, Help: o.Help, KeyMode: "button", Role: ui.RoleButton}, func(c *ui.Ctx) {
		c.Text(nil, label, uiText(c, 12, weight, fg))
	})
	return !o.Disabled && (in.Clicked || pressedKey(in.Keys))
}

// Chip is the uppercase status pill (base.css .chip): tone sets its text, border and tint.
func Chip(c *ui.Ctx, key any, label string, tone color.NRGBA, help string) ui.ID {
	return c.Row(key, ui.Style{Pad: ui.PadXY(7, 2), Cross: ui.Center, Fill: alpha(tone, .07), Stroke: alpha(tone, .35), Border: 1, Radius: 10, Help: help,
		Role: ui.RoleText, Label: label}, func(c *ui.Ctx) {
		c.Text(nil, strings.ToUpper(label), monoText(9.5, typeset.SemiBold, tone, .06))
	})
}
