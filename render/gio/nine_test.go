package gio

import (
	"image/color"
	"testing"

	"github.com/liamg/oriel/render"
)

// A translucent rounded fill is one colour across: its centre, filled plainly, meets the baked corners and edges
// without a seam. A pill's centre is a single row, which drew as a line across translucent chips (widget.Chip).
func TestTranslucentRoundedFillHasNoSeam(t *testing.T) {
	bg := color.NRGBA{R: 12, G: 12, B: 11, A: 255}
	tint := color.NRGBA{R: 0xb3, G: 0xab, B: 0x99, A: 18}
	hl, err := NewHeadless(120, 60, 2)
	if err != nil {
		t.Skipf("no offscreen GPU surface: %v", err)
	}
	defer hl.Release()
	hl.frame.Backdrop(bg)
	pill := render.Rect{Min: render.Point{X: 5, Y: 8.3}, Max: render.Point{X: 45, Y: 25.3}} // radius clamped to half its height
	card := render.Rect{Min: render.Point{X: 55, Y: 5}, Max: render.Point{X: 115, Y: 55}}   // a centre bigger than its corners
	if err := hl.Draw(func(f render.Frame) {
		f.Rect(render.Rect{Max: render.Point{X: 120, Y: 60}}, render.Style{Fill: bg})
		f.Rect(pill, render.Style{Fill: tint, Radius: 10})
		f.Rect(card, render.Style{Fill: tint, Radius: 6})
	}); err != nil {
		t.Fatal(err)
	}
	img, err := hl.Screenshot()
	if err != nil {
		t.Fatal(err)
	}
	at := func(x, y float32) color.RGBA { return img.RGBAAt(int(x*2), int(y*2)) }
	near := func(a, b color.RGBA) bool {
		d := func(x, y uint8) int { return max(int(x)-int(y), int(y)-int(x)) }
		return d(a.R, b.R) <= 2 && d(a.G, b.G) <= 2 && d(a.B, b.B) <= 2
	}
	mid := (pill.Min.Y + pill.Max.Y) / 2
	cx := (pill.Min.X + pill.Max.X) / 2
	for y := int(mid*2) - 3; y <= int(mid*2)+3; y++ {
		if got, want := img.RGBAAt(int(cx*2), y), at(cx, pill.Min.Y+4); !near(got, want) {
			t.Errorf("the pill's row %d is %v, its body %v", y, got, want)
		}
	}
	if centre, edge := at(85, 30), at(56.5, 30); !near(centre, edge) {
		t.Errorf("the card's centre is %v, its edge %v", centre, edge)
	}
}
