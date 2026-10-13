package widget

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/ui"
)

type TabsOpts struct{ Help string }

// underline is where a tab strip's underline glides from and to: x and width.
type underline struct {
	from, to [2]float32
	y        float32
	seen     bool
}

type underlineKey struct{ id ui.ID }

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
	// The underline glides from tab to tab: x and width move together, from where it was to the active tab's rect.
	ul := ui.State[underline](c, ui.Child(id, "underline"))
	if r, ok := c.Rect(ui.Child(id, *active)); ok {
		to := [2]float32{r.Min.X, r.Dx()}
		if !ul.seen {
			ul.from, ul.to, ul.seen = to, to, true
		}
		if to != ul.to {
			p := c.Motion(underlineKey{id}, 1)
			ul.from = [2]float32{ul.from[0] + (ul.to[0]-ul.from[0])*p, ul.from[1] + (ul.to[1]-ul.from[1])*p}
			ul.to = to
			c.ResetMotion(underlineKey{id}, 0)
		}
		ul.y = r.Max.Y - 2
	}
	p := c.Motion(underlineKey{id}, 1)
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
			c.Column(i, ui.Style{H: ui.Flex(1), Interactive: true, NoHover: true, Cursor: render.CursorPointer, Role: ui.RoleTab,
				Selected: on}, func(c *ui.Ctx) {
				c.Box("above", ui.Style{H: ui.Flex(1)}, nil)
				c.Text("label", l, monoText(10, typeset.SemiBold, fg, .1))
				c.Box("below", ui.Style{H: ui.Flex(1)}, nil)
				c.Box("edge", ui.Style{W: ui.Flex(1), H: ui.Px(2)}, nil)
			})
		}
		if ul.seen {
			at := render.Point{X: ul.from[0] + (ul.to[0]-ul.from[0])*p, Y: ul.y}
			c.Box("underline", ui.Style{W: ui.Px(ul.from[1] + (ul.to[1]-ul.from[1])*p), H: ui.Px(2), Fill: col(c, tTabEdge), Float: &ui.Float{Point: &at}}, nil)
		}
	})
	return *active != old
}
