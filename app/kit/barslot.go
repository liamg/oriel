package kit

import (
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui"
)

// BarSlot draws a slot of a bar: the views named slot.<order>-<name> (title-bar.right.40-usage) and the indicators in
// slot, together in order.
func BarSlot(c *ui.Ctx, h plugin.Host, slot string, vc uireg.ViewCtx) {
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
	inds, hud := IndicatorsOf(h)
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
