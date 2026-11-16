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

// A toned rule is drawn in its token's colour, across its row as a Rule is.
func TestRuleTone(t *testing.T) {
	var id ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 400, Y: 300}, func(c *ui.Ctx) {
		c.Column("col", ui.Style{W: ui.Px(200)}, func(c *ui.Ctx) { id = RuleTone(c, "toned", theme.Accent) })
	})
	h.frame()
	if r := h.rect(id); r.Dx() != 200 || r.Dy() != 1 {
		t.Errorf("the toned rule: %v", r)
	}
	want := h.u.Theme().C(theme.Accent)
	found := false
	for _, op := range h.list.Ops {
		if op.Kind == render.OpRect && op.Style.Fill == want {
			found = true
		}
	}
	if !found {
		t.Errorf("no rect drawn in the accent %v", want)
	}
}
