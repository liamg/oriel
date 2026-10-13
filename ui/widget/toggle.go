package widget

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tToggleOn   = theme.Declare("toggle.on", "accent")
	tToggleOff  = theme.Declare("toggle.off", "bg.strong")
	tToggleKnob = theme.Declare("toggle.knob", "fg")
)

type ToggleOpts struct {
	Label    string // its accessible name
	Disabled bool
	Help     string
}

// Toggle is an on/off switch showing on. It returns true on the frame it's flipped (clicked, or Enter or Space
// while focused); the caller keeps the value.
func Toggle(c *ui.Ctx, key any, on bool, o ToggleOpts) bool {
	id := c.Next(key)
	in := c.Interaction(id)
	track, side := col(c, tToggleOff), ui.Start
	if on {
		track, side = col(c, tToggleOn), ui.End
	}
	c.Row(key, ui.Style{W: ui.Px(32), H: ui.Px(18), Pad: ui.Pad(2), Main: side, Radius: 9, Fill: track, Interactive: true, Focusable: true,
		Disabled: o.Disabled, Cursor: render.CursorPointer, KeyMode: "button", Help: o.Help, Role: ui.RoleButton, Label: o.Label,
		Selected: on}, func(c *ui.Ctx) {
		c.Box("knob", ui.Style{W: ui.Px(14), H: ui.Px(14), Radius: 7, Fill: col(c, tToggleKnob)}, nil)
	})
	return !o.Disabled && (in.Clicked || pressedKey(in.Keys))
}

type SelectOpts struct {
	Label    string // its accessible name, and the menu's
	Disabled bool
	Help     string
	Width    float32 // the menu's; 220 if 0
}

type selectState struct{ open bool }

// Select is a button showing value that opens a menu of options. It returns the option chosen and true on the
// frame one is; the caller keeps the value.
func Select(c *ui.Ctx, key any, value string, options []string, o SelectOpts) (string, bool) {
	id := c.Next(key)
	st := ui.State[selectState](c, id)
	w := o.Width
	if w == 0 {
		w = 220
	}
	var chosen string
	var ok bool
	c.Box(key, ui.Style{}, func(c *ui.Ctx) {
		b := c.Next("drop")
		if Button(c, "drop", value+" ▾", ButtonOpts{Disabled: o.Disabled, Help: o.Help}) {
			st.open = true
		}
		Popover(c, "menu", b, &st.open, PopoverOpts{Label: o.Label, Width: w, Help: o.Help}, func(c *ui.Ctx) {
			items := make([]MenuItem, len(options))
			for i, v := range options {
				items[i] = MenuItem{Label: v, Help: o.Help}
			}
			if i, closed := Menu(c, "m", items, MenuOpts{Width: w, Help: o.Help}); i >= 0 {
				chosen, ok, st.open = options[i], true, false
			} else if closed {
				st.open = false
			}
		})
	})
	return chosen, ok
}
