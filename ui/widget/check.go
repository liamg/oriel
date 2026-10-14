package widget

import (
	"strings"

	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tCheckBorder = theme.Declare("check.border", "line.strong")
	tCheckOn     = theme.Declare("check.on", "ok")
	tCheckMixed  = theme.Declare("check.mixed", "amber")
	tCheckFg     = theme.Declare("check.fg", "fg.faint")
)

// CheckState is what a checkbox shows: off, on, or mixed (some of what it stands for is on).
type CheckState int

const (
	CheckOff CheckState = iota
	CheckOn
	CheckMixed
)

type CheckOpts struct {
	Text     string // shown beside the box, in small mono capitals; none if ""
	Label    string // its accessible name; Text if ""
	Disabled bool
	Help     string
	Tooltip  string
}

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
	in := Clickable(c, key, ui.Style{Dir: ui.Row, Gap: 6, Cross: ui.Center, Focusable: true, Disabled: o.Disabled, Selected: st == CheckOn,
		Label: name, Tooltip: o.Tooltip, Help: o.Help}, func(c *ui.Ctx) {
		box := ui.Style{W: ui.Px(13), H: ui.Px(13), Radius: 3, Border: 1, Stroke: col(c, tCheckBorder), Main: ui.Center, Cross: ui.Center, Clip: true}
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
				c.Text("t", "✓", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 9, Weight: typeset.Bold}, Color: on})
			case CheckMixed:
				c.Box("half", ui.Style{W: ui.Px(5.5), H: ui.Flex(1), Fill: alpha(mixed, .3)}, nil)
			}
		})
		if o.Text != "" {
			c.Text("l", strings.ToUpper(o.Text), monoText(10, typeset.SemiBold, fg, .06))
		}
	})
	return in.Clicked && !o.Disabled
}
