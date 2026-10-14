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
	Key      string // a key cap after the label: the key that does the same, "y"
}

// Button is a 26px push button (base.css .btn). It returns true on the frame it's clicked, or pressed with
// Enter or Space while focused.
func Button(c *ui.Ctx, key any, label string, o ButtonOpts) bool {
	id := c.Next(key)
	in := c.Interaction(id)
	passOn(c, id, in.Keys, activates)
	bg, hover, pressed, fg, border := col(c, tButtonBg), col(c, tButtonHover), col(c, tButtonPressed), col(c, tButtonFg), col(c, tButtonBorder)
	if o.Primary && !o.Disabled {
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
	c.Row(key, ui.Style{H: ui.Px(26), Pad: ui.PadXY(10, 0), Gap: 6, Cross: ui.Center, Fill: bg, EaseFill: true, Stroke: border, Border: 1, Radius: 6,
		Focusable: true, Disabled: o.Disabled, NoHover: true, Cursor: render.CursorPointer, Help: o.Help, KeyMode: "button", Role: ui.RoleButton}, func(c *ui.Ctx) {
		c.Text(nil, label, uiText(c, 12, weight, fg))
		if o.Key != "" {
			keyCap(c, o.Key, fg)
		}
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

type ToggleChipOpts struct {
	Help    string
	Tooltip string
	Label   string // its accessible name, when the label alone doesn't say it ("✕ 1"); the label by default
}

// ToggleChip is a small mono button that's lit while on, for a column or filter that toggles. It returns the value
// after this frame's clicks.
func ToggleChip(c *ui.Ctx, key any, label string, on bool, o ToggleChipOpts) bool {
	fg, border, fill := col(c, tSegFg), col(c, tSegBorder), color.NRGBA{}
	if on {
		fg, border, fill = col(c, tSegOnFg), alpha(col(c, tSegOnFg), .45), col(c, tSegOnBg)
	}
	name := o.Label
	if name == "" {
		name = label
	}
	in := Clickable(c, key, ui.Style{Dir: ui.Row, H: ui.Px(18), Pad: ui.PadXY(6, 0), Cross: ui.Center, Radius: 4, Stroke: border, Border: 1,
		Fill: fill, Focusable: true, Selected: on, Label: name, Tooltip: o.Tooltip, Help: o.Help}, func(c *ui.Ctx) {
		c.Text("l", label, monoText(10, typeset.Medium, fg, 0))
	})
	if in.Clicked {
		return !on
	}
	return on
}

// keyCap is a small key cap in tone, inside a button.
func keyCap(c *ui.Ctx, label string, tone color.NRGBA) {
	c.Row("key", ui.Style{Pad: ui.PadXY(4, 0), H: ui.Px(15), Cross: ui.Center, Stroke: alpha(tone, .4), Border: 1, Radius: 3}, func(c *ui.Ctx) {
		c.Text(nil, label, monoText(9.5, typeset.Medium, alpha(tone, .8), 0))
	})
}
