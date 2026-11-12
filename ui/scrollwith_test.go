package ui

import (
	"testing"

	"github.com/liamg/oriel/render"
)

// Boxes that scroll sideways with one ScrollWith move together: the wheel over any moves them all, as far as the
// widest one's content goes, and each keeps its content whole rather than cutting it with "…". Moving the shared
// id moves them too, and the offset holds where the widest has gone.
func TestScrollWithMovesBoxesTogether(t *testing.T) {
	u := newUI()
	shared := Child(0, "code")
	var rows [3]ID
	widths := [3]float32{400, 250, 120}
	build := func(c *Ctx) {
		c.Column("list", Style{W: Px(200), H: Px(300)}, func(c *Ctx) {
			for i, w := range widths {
				rows[i] = c.Row(i, Style{W: Flex(1), H: Px(20), ScrollX: true, ScrollWith: shared}, func(c *Ctx) {
					c.Box("text", Style{W: Px(w), H: Px(10)}, nil)
				})
			}
		})
	}
	size := render.Point{X: 300, Y: 400}
	paintOps(u, size, build)

	// The wheel over the short row moves every row: the long one's content is what can scroll.
	u.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: render.Point{X: 50, Y: 50}, Scroll: render.Point{X: 90}})
	paintOps(u, size, build)
	for i, id := range rows {
		if got := u.Scroll(id); got.X != 90 {
			t.Errorf("row %d at %v after the wheel over row 2, want 90", i, got)
		}
	}
	if r, _ := u.Rect(Child(rows[2], "text")); r.Min.X != -90 {
		t.Errorf("the short row's content is at %v, not moved with the rest", r.Min.X)
	}

	// It stops where the widest content's end shows: 400 wide in 200.
	u.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: render.Point{X: 50, Y: 10}, Scroll: render.Point{X: 1000}})
	paintOps(u, size, build)
	for i, id := range rows {
		if got := u.Scroll(id); got.X != 200 {
			t.Errorf("row %d at %v after a long wheel, want the widest's end 200", i, got)
		}
	}

	// Moving the shared id moves them all.
	u.ScrollBy(shared, -150, 0)
	paintOps(u, size, build)
	for i, id := range rows {
		if got := u.Scroll(id); got.X != 50 {
			t.Errorf("row %d at %v after ScrollBy on the shared id, want 50", i, got)
		}
	}

	// With the widest gone, the rest stop at their own widest.
	widths[0] = 100
	paintOps(u, size, build)
	paintOps(u, size, build)
	if got := u.Scroll(shared); got.X != 50 {
		t.Errorf("shared at %v once the widest narrowed, want 250-200=50", got)
	}
	widths[1] = 100
	paintOps(u, size, build)
	paintOps(u, size, build)
	if got := u.Scroll(rows[0]); got.X != 0 {
		t.Errorf("with nothing past the view the rows are at %v, want 0", got)
	}
}
