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

// A shrunk image keeps a thin line where it was and still dark; flat colour stays exact, and a part is as the whole.
func TestImageShrink(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 40, 4))
	for y := range 4 {
		for x := range 40 {
			v := uint8(255)
			if x == 21 {
				v = 0
			}
			src.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	ic := &imageCache{scales: newScaleCache(4), parts: newScaleCache(4), sources: sourceCache{m: map[image.Image]*image.RGBA{}}}
	out := ic.scaled(src, 20, 2)
	darkest := 0
	for x := range 20 {
		if out.RGBAAt(x, 1).R < out.RGBAAt(darkest, 1).R {
			darkest = x
		}
	}
	if c := out.RGBAAt(darkest, 1); darkest != 10 || c.R > 160 {
		t.Errorf("the darkest pixel is %d at %v, want 10 at 160 or darker", darkest, c.R)
	}
	if c := out.RGBAAt(2, 1); c.R != 255 || c.A != 255 {
		t.Errorf("white away from the line is %v", c)
	}
	whole, part := ic.scaled(src, 20, 2), ic.scaledPart(src, 20, 2, image.Rect(8, 0, 13, 2))
	for x := 8; x < 13; x++ {
		if whole.RGBAAt(x, 1) != part.RGBAAt(x-8, 1) {
			t.Errorf("a part's pixel %d is %v, the whole's %v", x, part.RGBAAt(x-8, 1), whole.RGBAAt(x, 1))
		}
	}
}
