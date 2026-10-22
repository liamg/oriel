package widget

import (
	"image"
	"image/color"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// Images in markdown are drawn in place: an image is drawn at the width it's given, its
// shape kept, in the device's pixels.
func TestImage(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 400, 200))
	for y := range 200 {
		for x := range 400 {
			if x < 200 {
				src.Set(x, y, color.NRGBA{R: 255, A: 255})
			} else {
				src.Set(x, y, color.NRGBA{B: 255, A: 255})
			}
		}
	}
	var id ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 640, Y: 300}, func(c *ui.Ctx) {
		c.Column("col", ui.Style{Pad: ui.Pad(20)}, func(c *ui.Ctx) {
			id = Image(c, "shot", src, 100, ImageOpts{Label: "the dashboard", Help: "test/image"})
		})
	})
	h.frame()
	if r := h.rect(id); r.Dx() != 100 || r.Dy() != 50 {
		t.Errorf("rect %v, want 100×50", r)
	}
	if n := h.access(id); n == nil || n.Role != ui.RoleImage || n.Name != "the dashboard" {
		t.Errorf("its accessible node: %+v", n)
	}
	var img *image.RGBA
	for _, op := range h.list.Ops {
		if op.Kind == render.OpImage {
			img = op.Image
			if op.At.X != 20 || op.At.Y != 20 {
				t.Errorf("drawn at %v", op.At)
			}
		}
	}
	if img == nil {
		t.Fatal("no image drawn")
	}
	if img.Rect.Dx() != 100 || img.Rect.Dy() != 50 {
		t.Errorf("scaled to %v", img.Rect)
	}
	if c := img.RGBAAt(10, 10); c.R != 255 || c.B != 0 {
		t.Errorf("left half %v", c)
	}
	if c := img.RGBAAt(90, 40); c.B != 255 || c.R != 0 {
		t.Errorf("right half %v", c)
	}
	first := img
	h.frame()
	for _, op := range h.list.Ops {
		if op.Kind == render.OpImage && op.Image != first {
			t.Error("scaled again for the same size")
		}
	}
}
