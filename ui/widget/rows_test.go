package widget

import (
	"testing"

	"github.com/liamg/oriel/render"
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

// Clicking a run row selects it; Open/Resume open it: with ClickSelects a click selects, Enter still opens.
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

// Rows hands up the keys it doesn't use: Escape after ↓ reaches the page the rows are on.
func TestRowsPassKeysOn(t *testing.T) {
	open, sel := true, 0
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		Page(c, "page", &open, PageOpts{KeyMode: "page", Help: "test/page"}, func(c *ui.Ctx) {
			id = c.Next("rows")
			Rows(c, "rows", 3, &sel, RowsOpts{Help: "test/rows", Label: "Rows"}, func(c *ui.Ctx, i int, selected bool) {
				c.Box("h", ui.Style{W: ui.Flex(1), H: ui.Px(20)}, nil)
			})
		})
	})
	h.frame()
	r := h.rect(id)
	h.click(r.Min.X+20, r.Min.Y+5)
	h.keys("Down")
	if sel != 1 {
		t.Fatalf("Down: sel %d", sel)
	}
	h.keys("Esc")
	h.frame()
	if open {
		t.Error("Escape on the rows didn't close the page")
	}
}

// Rows: a movable row dragged onto another is moved there, a click on one still opens it, and a row that isn't
// movable neither moves nor takes a move. Another list's rows don't drop in.
func TestRowsMovable(t *testing.T) {
	sel := 0
	var id, other ui.ID
	var res, otherRes ListResult
	var moves []Move
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("rows")
		res = Rows(c, "rows", 4, &sel, RowsOpts{Label: "Rows", Movable: func(i int) bool { return i != 3 }}, func(c *ui.Ctx, i int, selected bool) {
			c.Box("h", ui.Style{W: ui.Flex(1), H: ui.Px(20)}, nil)
		})
		if res.Moved != nil {
			moves = append(moves, *res.Moved)
		}
		other = c.Next("other")
		otherSel := 0
		otherRes = Rows(c, "other", 1, &otherSel, RowsOpts{Label: "Other", Movable: func(int) bool { return true }}, func(c *ui.Ctx, i int, selected bool) {
			c.Box("h", ui.Style{W: ui.Flex(1), H: ui.Px(20)}, nil)
		})
		if otherRes.Moved != nil {
			t.Errorf("a move into another list: %+v", *otherRes.Moved)
		}
	})
	h.frame()
	r := h.rect(id)
	at := func(i int) render.Point { return render.Point{X: r.Min.X + 20, Y: r.Min.Y + float32(i)*20 + 10} }
	h.drag(at(0), at(2))
	if len(moves) != 1 || moves[0] != (Move{From: 0, To: 2}) {
		t.Fatalf("row 0 dragged onto row 2: %+v", moves)
	}
	if res.Activated != -1 {
		t.Errorf("the drag also opened row %d", res.Activated)
	}
	h.drag(at(0), at(3))
	h.drag(at(3), at(1))
	if len(moves) != 1 {
		t.Errorf("a row that isn't movable moved, or took a move: %+v", moves)
	}
	o := h.rect(other)
	h.drag(render.Point{X: o.Min.X + 20, Y: o.Min.Y + 10}, at(1))
	if len(moves) != 1 {
		t.Errorf("another list's row dropped in: %+v", moves)
	}
	h.click(at(1).X, at(1).Y)
	if res.Activated != 1 {
		t.Errorf("a click on a movable row opened %d", res.Activated)
	}
}
