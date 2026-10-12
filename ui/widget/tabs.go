package widget

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/ui"
)

type TabsOpts struct{ Help string }

// Tabs is a strip of tabs with *active underlined in the accent. A click picks one; with the strip
// focused, Left/Right (or h/l) move.
func Tabs(c *ui.Ctx, key any, labels []string, active *int, o TabsOpts) bool {
	id := c.Next(key)
	in := c.Interaction(id)
	old := *active
	for _, k := range in.Keys {
		switch {
		case hasKey([]render.KeyEvent{k}, "l", "Right"):
			*active = min(*active+1, len(labels)-1)
		case hasKey([]render.KeyEvent{k}, "h", "Left"):
			*active = max(*active-1, 0)
		}
	}
	c.Row(key, ui.Style{H: ui.Px(28), Gap: 16, Pad: ui.PadXY(12, 0), Fill: col(c, tTabBg), Focusable: true, KeyMode: "tabs", Help: o.Help, Role: ui.RoleTabList}, func(c *ui.Ctx) {
		for i, l := range labels {
			ti := c.Interaction(c.Next(i))
			if ti.Clicked {
				*active = i
				c.Focus(id)
			}
			fg := col(c, tTabFg)
			if i == *active || ti.Hovered {
				fg = col(c, tTabActiveFg)
			}
			on := i == *active
			edge := render.Color{}
			if on {
				edge = col(c, tTabEdge)
			}
			c.Column(i, ui.Style{H: ui.Flex(1), Interactive: true, Cursor: render.CursorPointer, Role: ui.RoleTab, Selected: on}, func(c *ui.Ctx) {
				c.Box("above", ui.Style{H: ui.Flex(1)}, nil)
				c.Text("label", l, monoText(10, typeset.SemiBold, fg, .1))
				c.Box("below", ui.Style{H: ui.Flex(1)}, nil)
				c.Box("edge", ui.Style{W: ui.Flex(1), H: ui.Px(2), Fill: edge}, nil)
			})
		}
	})
	return *active != old
}
