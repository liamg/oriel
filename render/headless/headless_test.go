package headless

import (
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

func TestShotDrawsTheRecordedFrame(t *testing.T) {
	ts, err := typeset.NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	be := NewWith(ts)
	w, _ := be.NewWindow(render.WindowOpts{Width: 100, Height: 40})
	hw := w.(*Window)
	hw.Frame(func(f render.Frame) {
		f.Rect(render.XYWH(0, 0, 50, 40), render.Style{Fill: render.Color{R: 255, A: 255}})
		f.Glyphs(be.Shaper().Shape("Hi", typeset.FontSpec{Family: typeset.Mono, Size: 20}), render.Point{X: 60, Y: 30}, render.Color{G: 255, A: 255})
	})
	img := hw.Shot(hw.Frames()[0], render.Color{A: 255})
	if img.Bounds().Dx() != 100 || img.RGBAAt(10, 10).R != 255 {
		t.Errorf("shot %v, pixel %v", img.Bounds(), img.RGBAAt(10, 10))
	}
	green := 0
	for y := range 40 {
		for x := 60; x < 100; x++ {
			if img.RGBAAt(x, y).G > 128 {
				green++
			}
		}
	}
	if green == 0 {
		t.Error("no text in the shot")
	}
}
