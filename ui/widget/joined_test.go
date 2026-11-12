package widget

import (
	"image/color"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// A joined tab is its box filled, its top corners rounded and its foot flaring out a radius past each side into the
// surface under it, drawn under what's in the box.
func TestJoinedTab(t *testing.T) {
	red := color.NRGBA{R: 0xff, A: 0xff}
	var id ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 200, Y: 80}, func(c *ui.Ctx) {
		c.Column("pad", ui.Style{Pad: ui.Insets{L: 20, T: 10}}, func(c *ui.Ctx) {
			id = c.Box("tab", ui.Style{W: ui.Px(100), H: ui.Px(40), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
				Joined(c, "shape", red, 8)
				c.Text("name", "tab", ui.TextStyle{})
			})
		})
	})
	h.frame()
	if r := h.rect(id); r != (render.Rect{Min: render.Point{X: 20, Y: 10}, Max: render.Point{X: 120, Y: 50}}) {
		t.Fatalf("the tab's rect: %v", r)
	}
	shape, text := -1, -1
	for i, op := range h.list.Ops {
		if op.Kind == render.OpPath && op.Style.Fill == red && shape < 0 {
			shape = i
		}
		if op.Kind == render.OpGlyphs && op.Run.Text == "tab" {
			text = i
		}
	}
	if shape < 0 || text < 0 || shape > text {
		t.Fatalf("the shape (op %d) isn't drawn under the name (op %d)", shape, text)
	}
	img := raster.Render(h.list, h.size, 1, color.NRGBA{A: 0xff})
	for _, p := range []struct {
		x, y int
		in   bool
		what string
	}{
		{70, 30, true, "the middle"},
		{20, 10, false, "the top left corner, rounded off"},
		{119, 10, false, "the top right corner, rounded off"},
		{20, 49, true, "the bottom left corner, square"},
		{17, 49, true, "the left foot, past the side"},
		{122, 49, true, "the right foot, past the side"},
		{17, 43, false, "above the left foot's curve"},
		{122, 43, false, "above the right foot's curve"},
		{10, 49, false, "past the left foot"},
	} {
		if got := img.At(p.x, p.y).(color.RGBA).R > 0x80; got != p.in {
			t.Errorf("%s (%d,%d): filled %v, want %v", p.what, p.x, p.y, got, p.in)
		}
	}
}
