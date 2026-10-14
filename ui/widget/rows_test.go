package widget

import (
	"testing"

	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// Rows: rows of their own heights; j/k skip the static ones, Enter opens, a click selects and opens.
func TestRows(t *testing.T) {
	sel := 0
	var id ui.ID
	var res ListResult
	heights := []float32{40, 1, 23, 23}
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("rows")
		res = Rows(c, "rows", len(heights), &sel, RowsOpts{Help: "test/rows", Label: "Rows", Static: func(i int) bool { return i == 1 }},
			func(c *ui.Ctx, i int, selected bool) {
				c.Box("h", ui.Style{W: ui.Flex(1), H: ui.Px(heights[i])}, nil)
			})
	})
	h.frame()
	r := h.rect(id)
	h.click(r.Min.X+20, r.Min.Y+40+1+23+5)
	if sel != 3 || res.Activated != 3 || h.u.Focused() != id {
		t.Fatalf("a click on row 3: sel %d, activated %d, focused %v", sel, res.Activated, h.u.Focused() == id)
	}
	h.keys("k")
	if sel != 2 {
		t.Errorf("k from 3: %d", sel)
	}
	h.keys("k")
	if sel != 0 {
		t.Errorf("k from 2 skips the static row: %d", sel)
	}
	h.keys("j", "CR")
	if sel != 2 || res.Activated != 2 {
		t.Errorf("j then Enter: sel %d, activated %d", sel, res.Activated)
	}
	h.click(r.Min.X+20, r.Min.Y+40)
	if sel != 2 {
		t.Errorf("a click on the static row selected it: %d", sel)
	}
}

// Rows: a disabled row is shown and reached, but doesn't open.
func TestRowsDisabled(t *testing.T) {
	sel := 0
	var res ListResult
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("rows")
		res = Rows(c, "rows", 2, &sel, RowsOpts{Disabled: func(i int) bool { return i == 1 }}, func(c *ui.Ctx, i int, selected bool) {
			c.Box("h", ui.Style{W: ui.Flex(1), H: ui.Px(20)}, nil)
		})
	})
	h.frame()
	r := h.rect(id)
	h.click(r.Min.X+20, r.Min.Y+5)
	h.keys("j", "CR")
	if sel != 1 || res.Activated != -1 {
		t.Errorf("Enter on the disabled row: sel %d, activated %d", sel, res.Activated)
	}
	h.click(r.Min.X+20, r.Min.Y+25)
	if res.Activated != -1 {
		t.Errorf("a click opened the disabled row")
	}
}

// SE-5: "clicking a run row selects it; Open/Resume open it": with ClickSelects a click selects, Enter still opens.
func TestRowsClickSelects(t *testing.T) {
	sel, opened := 0, -1
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("rows")
		res := Rows(c, "rows", 3, &sel, RowsOpts{Help: "test/rows", Label: "Rows", ClickSelects: true}, func(c *ui.Ctx, i int, selected bool) {
			c.Box("h", ui.Style{W: ui.Flex(1), H: ui.Px(20)}, nil)
		})
		if res.Activated >= 0 {
			opened = res.Activated
		}
	})
	r := h.rect(id)
	h.click(r.Min.X+20, r.Min.Y+45)
	if sel != 2 || opened != -1 {
		t.Fatalf("a click on row 2: sel %d, opened %d", sel, opened)
	}
	h.keys("CR")
	if opened != 2 {
		t.Errorf("Enter on the selected row opened %d", opened)
	}
}
