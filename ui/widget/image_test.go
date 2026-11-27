package widget

import (
	"image"
	"image/color"
	"testing"
	"time"

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

// solid is a w×h picture of one colour.
func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return img
}

// twoFrames shows red for 100ms then blue for 200ms, over and over.
type twoFrames struct{ red, blue *image.RGBA }

func (f twoFrames) At(elapsed time.Duration) (image.Image, time.Duration) {
	t := elapsed % (300 * time.Millisecond)
	if t < 100*time.Millisecond {
		return f.red, 100*time.Millisecond - t
	}
	return f.blue, 300*time.Millisecond - t
}

// An animated image plays its frames from when it's first drawn, on the UI's clock, asking for a frame when the next
// is due; with ui.reduced_motion it stays on its still.
func TestImagePlaysItsFrames(t *testing.T) {
	frames := twoFrames{red: solid(10, 10, color.RGBA{R: 255, A: 255}), blue: solid(10, 10, color.RGBA{B: 255, A: 255})}
	h := newHarness(t, theme.Dark, render.Point{X: 200, Y: 100}, func(c *ui.Ctx) {
		Image(c, "anim", frames.red, 10, ImageOpts{Label: "a gif", Frames: frames})
	})
	o := h.u.Options()
	o.ReducedMotion = false
	h.u.SetOptions(o)
	drawn := func() color.RGBA {
		t.Helper()
		for _, op := range h.list.Ops {
			if op.Kind == render.OpImage {
				return op.Image.RGBAAt(5, 5)
			}
		}
		t.Fatal("no image drawn")
		return color.RGBA{}
	}
	h.frame()
	start := h.clock.Now()
	if c := drawn(); c.R != 255 {
		t.Errorf("first drawn %v, want the red frame", c)
	}
	if next := h.u.NextFrame(); !next.Equal(start.Add(100 * time.Millisecond)) {
		t.Errorf("asks for a frame at %v, want 100ms on, when blue is due", next.Sub(start))
	}
	h.clock.Set(start.Add(150 * time.Millisecond))
	h.frame()
	if c := drawn(); c.B != 255 {
		t.Errorf("150ms on drew %v, want the blue frame", c)
	}
	h.clock.Set(start.Add(320 * time.Millisecond))
	h.frame()
	if c := drawn(); c.R != 255 {
		t.Errorf("320ms on drew %v, want red again", c)
	}

	o.ReducedMotion = true
	h.u.SetOptions(o)
	h.clock.Set(start.Add(450 * time.Millisecond))
	h.frame()
	if c := drawn(); c.R != 255 {
		t.Errorf("with reduced motion drew %v, want the still", c)
	}
}
