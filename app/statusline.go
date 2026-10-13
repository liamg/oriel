package app

import (
	"context"
	"strings"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// The status line's segments are views named for their side and place: statusline.left.00-mode,
// statusline.right.90-position. Any extension adds one by registering a view so named; they sit in name order.
const (
	StatusLeft  = "statusline.left."
	StatusRight = "statusline.right."
)

const statusH = 26

// statusLine is the status line along the bottom (01-ide): its segments, left and right.
type statusLine struct{ disposer }

func (s *statusLine) Manifest() ext.Manifest { return ext.Manifest{ID: "statusline"} }

func (s *statusLine) Activate(_ context.Context, h ext.Host) error {
	s.add(h.View(StatusLineView, registry.ViewFunc(func(vc registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "statusline", Fn: func(c *ui.Ctx) {
			th := c.Theme()
			c.Row("statusline", ui.Style{W: ui.Flex(1), H: ui.Px(statusH), Gap: 16, Cross: ui.Center, Fill: th.C(tStatusBg), Stroke: th.C(theme.Line),
				Role: ui.RoleStatus, Label: "Status line"}, func(c *ui.Ctx) {
				ids := h.Views()
				segment := func(id string) {
					if v, ok := h.ViewByID(id); ok {
						c.Row(id, ui.Style{H: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) { c.Node(v.Build(vc)) })
					}
				}
				for _, id := range ids {
					if strings.HasPrefix(id, StatusLeft) {
						segment(id)
					}
				}
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				for _, id := range ids {
					if strings.HasPrefix(id, StatusRight) {
						segment(id)
					}
				}
				c.Box("end", ui.Style{W: ui.Px(0)}, nil)
			})
		}}
	})))
	return nil
}
