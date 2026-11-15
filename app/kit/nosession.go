package kit

import (
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// NoSessionNote says there's no session in front, and what shows once there is.
func NoSessionNote(c *ui.Ctx, what string) {
	c.Column("none", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center, Pad: ui.Pad(16)}, func(c *ui.Ctx) {
		c.Text("t", "No session in front: "+what, widget.UIFont(12.5, c.Theme().C(theme.FgFaint)))
	})
}
