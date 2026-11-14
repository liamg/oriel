package widget

import (
	"strings"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// Section is a block of a side panel: a kicker with a note at its right, then body, then a rule.
func Section(c *ui.Ctx, key, title, note string, body func(c *ui.Ctx)) {
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 14), Gap: 9, Role: ui.RoleGroup, Label: title}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("k", strings.ToUpper(title), KickerFont(th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if note != "" {
				c.Text("n", note, MonoFont(9.5, th.C(theme.FgGhost)))
			}
		})
		body(c)
	})
	Rule(c, key+".rule")
}

// Stat is a row of a Section: a name, and its value at the right in tone.
func Stat(c *ui.Ctx, key, name, value string, tone render.Color) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Cross: ui.Center, Role: ui.RoleText, Label: name + " " + value}, func(c *ui.Ctx) {
		c.Text("n", name, MonoFont(11.5, th.C(theme.FgMuted)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		c.Text("v", value, MonoFont(11.5, tone))
	})
}

// StatAgo is a Stat whose value is how long ago at was, then suffix.
func StatAgo(c *ui.Ctx, key any, name string, at time.Time, suffix string, tone render.Color) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Cross: ui.Center, Role: ui.RoleText, Label: name + " " + AgoLabel(c.Now(), at) + suffix}, func(c *ui.Ctx) {
		c.Text("n", name, MonoFont(11.5, th.C(theme.FgMuted)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		Ago(c, "v", at, AgoOpts{Text: MonoFont(11.5, tone), Suffix: suffix})
	})
}
