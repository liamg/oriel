package shell

import (
	"context"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/kit/draw"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// statusLine is the status line along the bottom (01-ide): its segments, left and right.
type statusLine struct {
	kit.Disposer
}

func (s *statusLine) Manifest() plugin.Manifest { return plugin.Manifest{ID: "status-line"} }

func (s *statusLine) Activate(_ context.Context, h plugin.Host) error {
	s.Add(h.View(kit.StatusLineView, uireg.ViewFunc(func(vc uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "statusline", Fn: func(c *ui.Ctx) {
			th := c.Theme()
			c.Row("statusline", ui.Style{W: ui.Flex(1), H: ui.Px(kit.StatusH), Gap: 16, Cross: ui.Center, Fill: th.C(tStatusBg), Stroke: th.C(theme.Line),
				Scale: draw.PartScale(h.Options(), "ui.statusline.font_size"),
				Role:  ui.RoleStatus, Label: "Status line"}, func(c *ui.Ctx) {
				kit.BarSlot(c, h, uireg.SlotStatusLeft, vc)
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				kit.BarSlot(c, h, uireg.SlotStatusRight, vc)
				c.Box("end", ui.Style{W: ui.Px(0)}, nil)
			})
		}}
	})))
	return nil
}
