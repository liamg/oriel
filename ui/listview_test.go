package ui

import (
	"slices"
	"testing"

	"github.com/liamg/oriel/render"
)

// A selection that moves past a List's edge scrolls the list to show it, even when its row wasn't built; between
// moves the list scrolls freely.
func TestKeepRowInView(t *testing.T) {
	u := newUI()
	sel := 0
	var built []int
	frame := func() {
		built = nil
		u.Layout(render.Point{X: 200, Y: 300}, func(c *Ctx) {
			id := c.List("list", Style{W: Flex(1), H: Px(100)}, 1000, 20, func(c *Ctx, i int) { built = append(built, i) })
			c.KeepRowInView(id, sel, 20)
		})
	}
	frame()
	frame()
	sel = 40
	frame()
	frame()
	if !slices.Contains(built, 40) || slices.Contains(built, 30) {
		t.Errorf("selecting row 40 built rows %v", built)
	}
	if got := u.Scroll(childID(0, "list")).Y; got != 41*20-100 {
		t.Errorf("scrolled to %v, want row 40 at the bottom (%v)", got, 41*20-100)
	}
	sel = 3
	frame()
	frame()
	if got := u.Scroll(childID(0, "list")).Y; got != 3*20 {
		t.Errorf("scrolled to %v, want row 3 at the top (%v)", got, 3*20)
	}
	u.ScrollBy(childID(0, "list"), 0, 400)
	frame()
	if got := u.Scroll(childID(0, "list")).Y; got != 3*20+400 {
		t.Errorf("the list scrolled back to the selection: at %v", got)
	}
}
