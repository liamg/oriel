package widget

import (
	"image"
	"image/color"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

func fill(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return img
}

// A Slider: the two overlaid, a draggable divider with the old left of it and the new right.
// The two are drawn at one scale.
func TestCompareDivider(t *testing.T) {
	red, blue := color.NRGBA{R: 255, A: 255}, color.NRGBA{B: 255, A: 255}
	var id ui.ID
	h := newHarness(t, theme.Dark, lightboxWindow, func(c *ui.Ctx) {
		c.Column("col", ui.Style{Pad: ui.Pad(20)}, func(c *ui.Ctx) {
			id = c.Next("cmp")
			Compare(c, "cmp", fill(400, 100, red), fill(400, 200, blue), 400, CompareOpts{Before: "Old", After: "New", Label: "logo.png", Help: "test/compare"})
		})
	})
	h.frame()
	r := h.rect(id)
	if r.Dx() != 400 || r.Dy() != 200 {
		t.Fatalf("rect %v, want 400×200", r)
	}
	isRed := func(c color.RGBA) bool { return c.R > 200 && c.B < 50 }
	isBlue := func(c color.RGBA) bool { return c.B > 200 && c.R < 50 }
	y := r.Min.Y + 50
	if c := h.pixel(render.Point{X: r.Min.X + 100, Y: r.Min.Y + 150}); isRed(c) || isBlue(c) {
		t.Errorf("under the shorter old image, left of the divider: %v, want neither", c)
	}
	if !isRed(h.pixel(render.Point{X: r.Min.X + 100, Y: y})) || !isBlue(h.pixel(render.Point{X: r.Min.X + 300, Y: y})) {
		t.Error("the divider doesn't start in the middle: old left, new right")
	}
	h.drag(render.Point{X: r.Min.X + 200, Y: y}, render.Point{X: r.Min.X + 340, Y: y})
	if !isRed(h.pixel(render.Point{X: r.Min.X + 300, Y: y})) || !isBlue(h.pixel(render.Point{X: r.Min.X + 380, Y: y})) {
		t.Error("dragged to 340, the old doesn't show left of it")
	}
	if n := h.access(id); n == nil || n.Value != "85%" {
		t.Errorf("its value: %+v", n)
	}
	h.keys("h", "h")
	if n := h.access(id); n.Value != "75%" {
		t.Errorf("h twice: %q", n.Value)
	}
	h.click(r.Max.X-8-14, r.Max.Y-8-14) // ⤢
	h.frame()
	if !h.drew("Old") || !h.drew("1 / 2") || h.dialog() == nil {
		t.Errorf("⤢ doesn't show the two in the lightbox: %q", h.texts())
	}
}
