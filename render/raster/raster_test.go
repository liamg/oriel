package raster

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

var (
	black = color.NRGBA{A: 255}
	white = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	red   = color.NRGBA{R: 255, A: 255}
)

func draw(w, h int, scale float32, fn func(f render.Frame)) *image.RGBA {
	var l render.List
	fn(l.Record(scale))
	return Render(&l, render.Point{X: float32(w) / scale, Y: float32(h) / scale}, scale, black)
}

func at(img *image.RGBA, x, y int) color.RGBA { return img.RGBAAt(x, y) }

func TestFillSnapsToPixels(t *testing.T) {
	img := draw(10, 10, 1, func(f render.Frame) {
		f.Rect(render.XYWH(2.4, 2.6, 3, 3), render.Style{Fill: white})
	})
	// 2.4 rounds to 2 and 5.4 to 5; 2.6 to 3 and 5.6 to 6.
	for y := range 10 {
		for x := range 10 {
			in := x >= 2 && x < 5 && y >= 3 && y < 6
			if got := at(img, x, y).R; (got == 255) != in || (got != 0 && got != 255) {
				t.Fatalf("pixel %d,%d = %d; inside: %v", x, y, got, in)
			}
		}
	}
}

func TestTranslucentFillBlendsInSRGB(t *testing.T) {
	img := draw(1, 1, 1, func(f render.Frame) {
		f.Rect(render.XYWH(0, 0, 1, 1), render.Style{Fill: color.NRGBA{R: 255, G: 255, B: 255, A: 128}})
	})
	if got := at(img, 0, 0); got.R != 128 {
		t.Errorf("50%% white over black = %v, want 128 (CSS blends in sRGB)", got)
	}
}

func TestRoundedCornersAndBorders(t *testing.T) {
	img := draw(20, 20, 1, func(f render.Frame) {
		f.Rect(render.XYWH(0, 0, 20, 20), render.Style{Fill: white, Radius: 6})
	})
	if c := at(img, 0, 0).R; c != 0 {
		t.Errorf("corner pixel %d, want clear", c)
	}
	if c := at(img, 10, 10).R; c != 255 {
		t.Errorf("middle %d, want full", c)
	}
	if c := at(img, 1, 2).R; c == 0 || c == 255 {
		t.Errorf("pixel on the curve %d, want partial", c)
	}
	for _, p := range [][2]int{{0, 0}, {1, 2}, {2, 1}, {0, 10}} {
		if a, b := at(img, p[0], p[1]), at(img, 19-p[0], 19-p[1]); a != b {
			t.Errorf("corners aren't symmetric: %v at %v, %v opposite", a, p, b)
		}
	}
	border := draw(20, 20, 1, func(f render.Frame) {
		f.Rect(render.XYWH(0, 0, 20, 20), render.Style{Stroke: white, Width: 2})
	})
	if at(border, 0, 10).R != 255 || at(border, 1, 10).R != 255 || at(border, 2, 10).R != 0 || at(border, 10, 10).R != 0 {
		t.Error("a 2px border sits inside the rect")
	}
}

func TestClipAndOpacityLayers(t *testing.T) {
	img := draw(10, 10, 1, func(f render.Frame) {
		f.PushClip(render.XYWH(0, 0, 5, 10), 0)
		f.PushOpacity(0.5)
		f.Rect(render.XYWH(0, 0, 10, 10), render.Style{Fill: red})
		f.Rect(render.XYWH(0, 0, 10, 10), render.Style{Fill: white})
		f.Pop()
		f.Pop()
	})
	// The layer composites as one: half of white, not half of red then half of white.
	if got := at(img, 2, 2); got.R != 128 || got.G != 128 {
		t.Errorf("inside: %v, want the layer's white at half", got)
	}
	if got := at(img, 7, 2); got.R != 0 {
		t.Errorf("outside the clip: %v", got)
	}
}

func TestHairlineStaysThin(t *testing.T) {
	img := draw(10, 10, 1, func(f render.Frame) {
		f.Path(new(render.Path).MoveTo(render.Point{X: 0, Y: 5}).LineTo(render.Point{X: 10, Y: 5}), render.Style{Stroke: white, Width: 0.5})
	})
	var total float64
	for y := range 10 {
		total += float64(at(img, 5, y).R) / 255
	}
	if math.Abs(total-0.5) > 0.1 {
		t.Errorf("a 0.5px line covers %v of a column, want 0.5", total)
	}
}

func TestGradientIsPremultiplied(t *testing.T) {
	img := draw(100, 1, 1, func(f render.Frame) {
		f.Rect(render.XYWH(0, 0, 100, 1), render.Style{Gradient: &render.Gradient{From: color.NRGBA{R: 255, A: 0}, To: white, Horizontal: true}})
	})
	// A transparent red stop contributes no red: the midpoint is half-transparent white, not pink.
	if got := at(img, 50, 0); got.R != got.G {
		t.Errorf("midpoint %v is tinted by a transparent stop", got)
	}
}

func TestGlyphsAndScale(t *testing.T) {
	ts, err := typeset.NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	run := ts.Shape("H", typeset.FontSpec{Family: typeset.Mono, Size: 20})
	ink := func(scale float32) int {
		img := draw(int(40*scale), int(40*scale), scale, func(f render.Frame) { f.Glyphs(run, render.Point{X: 5, Y: 30}, white) })
		n := 0
		for i := 0; i < len(img.Pix); i += 4 {
			if img.Pix[i] > 128 {
				n++
			}
		}
		return n
	}
	one, two := ink(1), ink(2)
	if one == 0 || math.Abs(float64(two)/float64(one)-4) > 0.6 {
		t.Errorf("H inks %d pixels at 1× and %d at 2×; want about 4×", one, two)
	}
}
