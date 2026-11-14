package widget

import (
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// A rule spans its row one pixel high, and a VRule its column one pixel wide.
func TestRules(t *testing.T) {
	var across, down ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 400, Y: 300}, func(c *ui.Ctx) {
		c.Column("col", ui.Style{W: ui.Px(200), H: ui.Px(100)}, func(c *ui.Ctx) {
			across = Rule(c, "across")
			c.Row("row", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) { down = VRule(c, "down") })
		})
	})
	h.frame()
	if r := h.rect(across); r.Dx() != 200 || r.Dy() != 1 {
		t.Errorf("the rule: %v", r)
	}
	if r := h.rect(down); r.Dx() != 1 || r.Dy() != 99 {
		t.Errorf("the vertical rule: %v", r)
	}
}
