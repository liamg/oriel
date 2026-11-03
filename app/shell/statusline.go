package shell

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// The status line's segments (kit.StatusLeft, kit.StatusRight).
const (
	StatusLeft  = kit.StatusLeft
	StatusRight = kit.StatusRight
)

// statusLine is the status line along the bottom (01-ide): its segments, left and right.
type statusLine struct {
	kit.Disposer
	sh *shell
}

func (s *statusLine) Manifest() plugin.Manifest { return plugin.Manifest{ID: "statusline"} }

func (s *statusLine) Activate(_ context.Context, h plugin.Host) error {
	s.Add(h.View(kit.StatusLineView, uireg.ViewFunc(func(vc uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "statusline", Fn: func(c *ui.Ctx) {
			th := c.Theme()
			c.Row("statusline", ui.Style{W: ui.Flex(1), H: ui.Px(kit.StatusH), Gap: 16, Cross: ui.Center, Fill: th.C(tStatusBg), Stroke: th.C(theme.Line),
				Scale: kit.PartScale(h.Options(), "ui.statusline.font_size"),
				Role:  ui.RoleStatus, Label: "Status line"}, func(c *ui.Ctx) {
				slot(c, h, uireg.SlotStatusLeft, vc)
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				slot(c, h, uireg.SlotStatusRight, vc)
				c.Box("end", ui.Style{W: ui.Px(0)}, nil)
			})
		}}
	})))
	return nil
}

// slot draws a slot of a bar: the views named slot.<order>-<name> (titlebar.right.40-usage) and the indicators in
// slot, together in order.
func slot(c *ui.Ctx, h plugin.Host, slot string, vc uireg.ViewCtx) {
	type part struct {
		key  string
		view uireg.View
		ind  *uireg.Indicator
	}
	var parts []part
	for _, id := range h.Views() {
		if strings.HasPrefix(id, slot+".") {
			if v, ok := h.ViewByID(id); ok && (!vc.Global || uireg.IsGlobal(v)) {
				parts = append(parts, part{key: id, view: v})
			}
		}
	}
	inds, hud := kit.IndicatorsOf(h)
	if hud {
		for _, ind := range uireg.Indicators(h, slot) {
			if vc.Global && !ind.Global {
				continue
			}
			parts = append(parts, part{key: fmt.Sprintf("%s.%02d-%s", slot, ind.Order, ind.Name), ind: &ind})
		}
	}
	slices.SortStableFunc(parts, func(a, b part) int { return strings.Compare(a.key, b.key) })
	for _, p := range parts {
		var n ui.Node
		if p.view != nil {
			n = p.view.Build(vc)
		} else {
			n = inds.Node(c, *p.ind)
		}
		if n != nil {
			c.Row(p.key, ui.Style{H: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) { c.Node(n) })
		}
	}
}
