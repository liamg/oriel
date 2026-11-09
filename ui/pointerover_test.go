package ui

import (
	"testing"

	"github.com/liamg/oriel/render"
)

// PointerOver says the pointer is on a box that takes no pointer itself, inside one that does (an editor's row on
// its surface); not on the box beside it, and not on it under a float that takes the pointer there.
func TestPointerOverABoxThatTakesNoPointer(t *testing.T) {
	u := newUI()
	var over [2]bool
	cover := false
	build := func(c *Ctx) {
		c.Column("surface", Style{W: Px(200), H: Px(100), Interactive: true}, func(c *Ctx) {
			for i := range 2 {
				id := c.Next(i)
				over[i] = c.PointerOver(id)
				c.Box(i, Style{W: Flex(1), H: Px(20)}, nil)
			}
		})
		if cover {
			c.Box("dialog", Style{W: Px(200), H: Px(100), Interactive: true, Z: LayerDialog, Float: &Float{}}, nil)
		}
	}
	frame := func() { u.Frame(new(render.List).Record(1), render.Point{X: 400, Y: 300}, build) }
	frame()
	u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 50, Y: 30}})
	frame()
	frame()
	if over != [2]bool{false, true} {
		t.Errorf("with the pointer on the second row, over = %v", over)
	}
	cover = true
	frame()
	frame()
	if over[1] {
		t.Error("the row is over under a dialog that takes the pointer")
	}
}
