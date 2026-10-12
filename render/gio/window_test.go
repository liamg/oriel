package gio

import (
	"os"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

var fontSpec = typeset.FontSpec{Family: typeset.Mono, Size: 13}

// TestWindow opens a real window, so it needs a display: CI runs it under Xvfb.
func TestWindow(t *testing.T) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("no display")
	}
	b, err := New()
	if err != nil {
		t.Fatal(err)
	}
	w, err := b.NewWindow(render.WindowOpts{Title: "oriel test", Width: 400, Height: 300})
	if err != nil {
		t.Fatal(err)
	}
	if w.Scale() <= 0 {
		t.Fatalf("scale %v", w.Scale())
	}
	if err := w.Frame(func(f render.Frame) {
		f.Rect(render.XYWH(0, 0, 400, 300), render.Style{Fill: render.Color{R: 6, G: 6, B: 6, A: 255}})
		f.Glyphs(b.Shaper().Shape("hello", fontSpec), render.Point{X: 10, Y: 20}, render.Color{R: 255, G: 255, B: 255, A: 255})
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-w.Events():
			if rs, ok := ev.(render.ResizeEvent); ok {
				if rs.Size.X <= 0 || rs.Size.Y <= 0 || w.Size() != rs.Size {
					t.Errorf("resized to %v, Size() %v", rs.Size, w.Size())
				}
				w.Close()
				for range w.Events() {
				}
				return
			}
		case <-deadline:
			t.Fatal("the window never drew a frame")
		}
	}
}
