package app

import (
	"context"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// The status line's segments are views named for their side and place: statusline.left.00-mode,
// statusline.right.90-position. Any extension adds one by registering a view so named; they sit in name order. A
// segment with nothing to show builds no node, and takes no room.
const (
	StatusLeft  = "statusline.left."
	StatusRight = "statusline.right."
)

const statusH = 26

// statusLine is the status line along the bottom (01-ide): its segments, left and right.
type statusLine struct {
	disposer
	sh *shell
}

func (s *statusLine) Manifest() ext.Manifest { return ext.Manifest{ID: "statusline"} }

func (s *statusLine) Activate(_ context.Context, h ext.Host) error {
	s.add(h.View(StatusLineView, registry.ViewFunc(func(vc registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "statusline", Fn: func(c *ui.Ctx) {
			th := c.Theme()
			c.Row("statusline", ui.Style{W: ui.Flex(1), H: ui.Px(statusH), Gap: 16, Cross: ui.Center, Fill: th.C(tStatusBg), Stroke: th.C(theme.Line),
				Scale: partScale(h.Options(), "ui.statusline.font_size"),
				Role:  ui.RoleStatus, Label: "Status line"}, func(c *ui.Ctx) {
				s.sh.slot(c, h, registry.SlotStatusLeft, vc)
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				s.sh.slot(c, h, registry.SlotStatusRight, vc)
				c.Box("end", ui.Style{W: ui.Px(0)}, nil)
			})
		}}
	})))
	return nil
}
