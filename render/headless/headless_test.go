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

// A zoom multiplies the device scale, so the app lays out at the window's size over it.
func TestZoomScalesTheWindow(t *testing.T) {
	be := New()
	w, _ := be.NewWindow(render.WindowOpts{Width: 1500, Height: 900})
	hw := w.(*Window)
	hw.SetZoom(1.5)
	if hw.Scale() != 1.5 || hw.Size() != (render.Point{X: 1000, Y: 600}) {
		t.Fatalf("at 1.5×: scale %v, size %v", hw.Scale(), hw.Size())
	}
	select {
	case ev := <-hw.Events():
		if rs, ok := ev.(render.ResizeEvent); !ok || rs.Size != (render.Point{X: 1000, Y: 600}) {
			t.Errorf("event %#v, want a resize to the zoomed size", ev)
		}
	default:
		t.Error("no resize for the app")
	}
	hw.Frame(func(f render.Frame) {
		if f.Scale() != 1.5 {
			t.Errorf("the frame records at %v", f.Scale())
		}
	})
	if img := hw.Shot(hw.Frames()[0], render.Color{A: 255}); img.Bounds().Dx() != 1500 {
		t.Errorf("the shot is %v wide, want the window's 1500 device pixels", img.Bounds().Dx())
	}
}
