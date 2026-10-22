package kit

import (
	"strings"

	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// PreviewHead is a preview's heading: a chip, then a title.
func PreviewHead(c *ui.Ctx, chip string, tone ui.TextStyle, title string) {
	th := c.Theme()
	if chip != "" {
		widget.Chip(c, "chip", chip, tone.Color, "shell/omnibox")
	}
	c.Text("title", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 17, Weight: typeset.Medium}, Color: th.C(theme.Fg)})
}

// PreviewField is a label and its value, as the preview lists them.
func PreviewField(c *ui.Ctx, label, value string) {
	th := c.Theme()
	c.Row(label, ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
		c.Box("l", ui.Style{W: ui.Px(88)}, func(c *ui.Ctx) {
			c.Text(nil, strings.ToUpper(label), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Tracking: .12}, Color: th.C(theme.FgFaint)})
		})
		c.Text("v", value, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.Fg)})
	})
}

// KeyHint is a key cap and what it does.
func KeyHint(c *ui.Ctx, key any, k, what string) {
	th := c.Theme()
	c.Row(key, ui.Style{Gap: 5, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Row("cap", ui.Style{Pad: ui.PadXY(4, 0), H: ui.Px(15), Cross: ui.Center, Stroke: th.C(theme.LineStrong), Border: 1, Radius: 3}, func(c *ui.Ctx) {
			c.Text("k", k, MonoFont(9.5, th.C(theme.FgMuted)))
		})
		c.Text("t", what, UIFont(11, th.C(theme.FgFaint)))
	})
}
