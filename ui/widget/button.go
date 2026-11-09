package widget

import (
	"image/color"
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

type ButtonOpts struct {
	Primary  bool
	Ghost    bool // no fill until hovered
	Disabled bool
	Why      string // while Disabled, why it can't be pressed: its tooltip
	Help     string
	Key      string // a key cap after the label: the key that does the same, "y"
	// Busy shows a spinner in place of the key cap and disables the button, keeping its width, while its work runs.
	Busy      bool
	BusyLabel string // the label while Busy: "Starting"; the "…" is added
	// Tone is a primary button's colour in place of the accent: a GitHub action's violet.
	Tone color.NRGBA
}

// Button is a 26px push button (base.css .btn). It returns true on the frame it's clicked, or pressed with
// Enter or Space while focused.
func Button(c *ui.Ctx, key any, label string, o ButtonOpts) bool {
	id := c.Next(key)
	in := c.Interaction(id)
	passOn(c, id, in.Keys, activates)
	wide := ui.State[float32](c, id)
	w := ui.Size{}
	if o.Busy {
		o.Disabled = true
		w.Min = *wide
		if o.BusyLabel != "" {
			label = strings.TrimSuffix(o.BusyLabel, "…") + "…"
		}
	} else if r, ok := c.Rect(id); ok {
		*wide = r.Dx()
	}
	bg, hover, pressed, fg, border := col(c, tButtonBg), col(c, tButtonHover), col(c, tButtonPressed), col(c, tButtonFg), col(c, tButtonBorder)
	if o.Primary && !o.Disabled {
		bg, hover, pressed, fg = col(c, tPrimaryBg), col(c, tPrimaryHover), col(c, tPrimaryPressed), col(c, tPrimaryFg)
		if o.Tone.A != 0 {
			bg, hover, pressed = o.Tone, theme.Lighten(o.Tone, .04), theme.Lighten(o.Tone, -.06)
		}
		border = bg
	}
	switch {
	case o.Busy:
		fg = col(c, tButtonBusyFg)
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
	c.Row(key, ui.Style{W: w, H: ui.Px(26), Pad: ui.PadXY(10, 0), Gap: 6, Cross: ui.Center, Fill: bg, EaseFill: true, Stroke: border, Border: 1, Radius: 6,
		Focusable: true, Disabled: o.Disabled, NoHover: true, Sink: true, Cursor: render.CursorPointer, Help: o.Help, KeyMode: "button", Role: ui.RoleButton, Tooltip: why(o)}, func(c *ui.Ctx) {
		if o.Busy {
			c.Box("busy", ui.Style{Enter: fadeIn, Exit: fadeIn}, func(c *ui.Ctx) { Spinner(c, "busy", SpinnerOpts{Size: 11, Tone: fg, Name: label}) })
		}
		c.Text(nil, label, uiText(c, 12, weight, fg))
		if o.Key != "" && !o.Busy {
			c.Box("cap", ui.Style{Enter: fadeIn, Exit: fadeIn}, func(c *ui.Ctx) { keyCap(c, o.Key, fg) })
		}
	})
	return !o.Disabled && (in.Clicked || pressedKey(in.Keys))
}

// why is a button's tooltip: why it can't be pressed, while it can't for a reason other than its own work running.
func why(o ButtonOpts) string {
	if o.Disabled && !o.Busy {
		return o.Why
	}
	return ""
}

// fadeIn is a part of a control that fades as it comes and goes: a busy button's spinner, its key cap.
var fadeIn = &ui.Transition{Fade: true}

// Chip is the uppercase status pill (base.css .chip): tone sets its text, border and tint.
func Chip(c *ui.Ctx, key any, label string, tone color.NRGBA, help string) ui.ID {
	return c.Row(key, ui.Style{Pad: ui.PadXY(7, 2), Cross: ui.Center, Fill: alpha(tone, .07), Stroke: alpha(tone, .35), Border: 1, Radius: 10, Help: help,
		Role: ui.RoleText, Label: label, Keep: true}, func(c *ui.Ctx) {
		c.Text(nil, strings.ToUpper(label), monoText(9.5, typeset.SemiBold, tone, .06))
	})
}

type ToggleChipOpts struct {
	Help    string
	Tooltip string
	Label   string                           // its accessible name, when the label alone doesn't say it ("✕ 1"); the label by default
	Lead    func(c *ui.Ctx, fg render.Color) // drawn before the label, in its colour: an icon
	Tone    color.NRGBA                      // a status colour for its text and edge, as a Chip's: a severity filter
}

// ToggleChip is a small mono button that's lit while on, for a column or filter that toggles. It returns the value
// after this frame's clicks.
func ToggleChip(c *ui.Ctx, key any, label string, on bool, o ToggleChipOpts) bool {
	fg, border, fill := col(c, tSegFg), col(c, tSegBorder), color.NRGBA{}
	switch {
	case o.Tone != (color.NRGBA{}) && on:
		fg, border, fill = o.Tone, o.Tone, alpha(o.Tone, .2)
	case o.Tone != (color.NRGBA{}):
		fg, border, fill = o.Tone, alpha(o.Tone, .35), alpha(o.Tone, .07)
	case on:
		fg, border, fill = col(c, tSegOnFg), alpha(col(c, tSegOnFg), .45), col(c, tSegOnBg)
	}
	name := o.Label
	if name == "" {
		name = label
	}
	in := Clickable(c, key, ui.Style{Dir: ui.Row, H: ui.Px(18), Pad: ui.PadXY(6, 0), Cross: ui.Center, Radius: 4, Stroke: border, Border: 1,
		Fill: fill, Focusable: true, Selected: on, Label: name, Tooltip: o.Tooltip, Help: o.Help, Gap: 4}, func(c *ui.Ctx) {
		if o.Lead != nil {
			o.Lead(c, fg)
		}
		if label != "" {
			c.Text("l", label, monoText(10, typeset.Medium, fg, 0))
		}
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
