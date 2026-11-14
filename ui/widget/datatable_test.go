package widget

import (
	"slices"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// A data card is a group named by its kicker; its header's first column starts left and the rest end right, its cells
// line up under them, and a KV row says its name and puts its last value furthest right.
func TestDataCard(t *testing.T) {
	cols := []DataCol{{Title: "REPO"}, {Title: "COST", W: 60}}
	h := newHarness(t, theme.Dark, render.Point{X: 400, Y: 200}, func(c *ui.Ctx) {
		DataCard(c, "card", ui.Px(300), "BY REPO", "this week", nil, func(c *ui.Ctx) {
			DataHead(c, cols)
			c.Row("r", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
				DataCell(c, "name", cols[0].W, true, func(c *ui.Ctx) { c.Text("t", "api", ui.TextStyle{}) })
				DataText(c, "cost", cols[1].W, "$4.20", render.Color{A: 255})
			})
			KV(c, "kv", render.Color{R: 255, A: 255}, "opus", "", "12", "$3.10")
		})
	})
	var group func(n *render.AccessNode) bool
	group = func(n *render.AccessNode) bool {
		return n != nil && (n.Role == render.RoleGroup && n.Name == "BY REPO" || slices.ContainsFunc(n.Children, group))
	}
	if !group(h.u.AccessTree()) {
		t.Error("the card isn't a group named BY REPO")
	}
	repo, cost, price := h.glyphs("REPO"), h.glyphs("COST"), h.glyphs("$4.20")
	if repo.At.X > 30 {
		t.Errorf("the first column's title is at %v, want it at the left", repo.At.X)
	}
	if !nearPx(cost.At.X+cost.Run.Advance, price.At.X+price.Run.Advance) {
		t.Errorf("COST ends at %v and its cell at %v: want them right-aligned together", cost.At.X+cost.Run.Advance, price.At.X+price.Run.Advance)
	}
	if v1, v2 := h.glyphs("12"), h.glyphs("$3.10"); v2.At.X <= v1.At.X {
		t.Errorf("KV's last value at %v isn't right of the one before at %v", v2.At.X, v1.At.X)
	}
}
