package widget

import (
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tCheckBorder = theme.Declare("check.border", "line.strong")
	tCheckOn     = theme.Declare("check.on", "ok")
	tCheckMixed  = theme.Declare("check.mixed", "amber")
	tCheckFg     = theme.Declare("check.fg", "fg.faint")
	tCheckText   = theme.Declare("check.text", "fg")
)

// CheckState is what a checkbox shows: off, on, or mixed (some of what it stands for is on).
type CheckState int

const (
	CheckOff CheckState = iota
	CheckOn
	CheckMixed
)

type CheckOpts struct {
	Text     string // shown beside the box, in small mono capitals (as written when Plain); none if ""
	Plain    bool   // Text as written, in the body's colour: a file name, a choice
	Label    string // its accessible name; Text if ""
	Disabled bool
	Help     string
	Tooltip  string
}

// tickIn is a check's tick coming in: it fades in dropping 2 px.
var tickIn = &ui.Transition{Fade: true, Shift: render.Point{Y: -2}}

// Check is a checkbox showing st, with Text beside it lit while it's on. It returns true on the frame it's clicked
// (or Enter or Space while focused); the caller keeps the state.
func Check(c *ui.Ctx, key any, st CheckState, o CheckOpts) bool {
	name := o.Label
	if name == "" {
		name = o.Text
	}
	on, mixed := col(c, tCheckOn), col(c, tCheckMixed)
	fg := col(c, tCheckFg)
	if st == CheckOn {
		fg = on
	}
	var checked *bool // mixed is neither
	if st != CheckMixed {
		checked = new(st == CheckOn)
	}
	in := Clickable(c, key, ui.Style{Dir: ui.Row, Gap: 6, Cross: ui.Center, Focusable: true, Disabled: o.Disabled, Role: ui.RoleCheckBox, Checked: checked,
		Label: name, Tooltip: o.Tooltip, Help: o.Help}, func(c *ui.Ctx) {
		box := ui.Style{W: ui.Px(13), H: ui.Px(13), Radius: 3, Border: 1, Stroke: col(c, tCheckBorder), Main: ui.Center, Cross: ui.Center, Clip: true, EaseFill: true}
		switch st {
		case CheckOn:
			box.Stroke, box.Fill = on, alpha(on, .15)
		case CheckMixed:
			box.Stroke = mixed
			box.Main = ui.Start
		}
		c.Row("box", box, func(c *ui.Ctx) {
			switch st {
			case CheckOn:
				c.Box("tick", ui.Style{Enter: tickIn, Exit: fadeIn}, func(c *ui.Ctx) {
					c.Text("t", "✓", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 9, Weight: typeset.Bold}, Color: on})
				})
			case CheckMixed:
				c.Box("half", ui.Style{W: ui.Px(5.5), H: ui.Flex(1), Fill: alpha(mixed, .3), Enter: fadeIn}, nil)
			}
		})
		switch {
		case o.Text == "":
		case o.Plain:
			fg := col(c, tCheckText)
			if o.Disabled {
				fg = col(c, tDisabledFg)
			}
			c.Text("l", o.Text, monoText(11.5, typeset.Normal, fg, 0))
		default:
			c.Text("l", strings.ToUpper(o.Text), monoText(10, typeset.SemiBold, fg, .06))
		}
	})
	return in.Clicked && !o.Disabled
}
