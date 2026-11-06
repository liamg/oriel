package gio

import (
	"testing"

	"gioui.org/op"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

// What's out of the window, or out of the clips pushed, isn't drawn: no glyphs or rects for the GPU to cover.
func TestFrameDrawsOnlyWhatShows(t *testing.T) {
	ts, err := typeset.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	run := ts.Shape("hello", typeset.FontSpec{Family: typeset.UI, Size: 13})
	white := render.Color{R: 255, G: 255, B: 255, A: 255}
	f := NewFrame()
	draw := func(fn func()) int {
		t.Helper()
		var ops op.Ops
		f.Begin(&ops, 2)
		f.Window(render.Point{X: 200, Y: 100})
		fn()
		f.End()
		return f.drawn
	}
	if n := draw(func() { f.Glyphs(run, render.Point{X: 10, Y: 20}, white) }); n != len(run.Glyphs) {
		t.Errorf("a run in the window drew %d glyphs, want %d", n, len(run.Glyphs))
	}
	if n := draw(func() { f.Glyphs(run, render.Point{X: 10, Y: 2000}, white) }); n != 0 {
		t.Errorf("a run below the window drew %d glyphs", n)
	}
	if n := draw(func() {
		f.PushClip(render.XYWH(0, 0, 200, 40), 0)
		f.Glyphs(run, render.Point{X: 10, Y: 80}, white)
		f.Rect(render.XYWH(0, 60, 200, 20), render.Style{Fill: white})
		f.Pop()
		f.Rect(render.XYWH(0, 60, 200, 20), render.Style{Fill: white})
	}); n != 1 {
		t.Errorf("past a clip, %d glyphs and rects drawn; want only the rect drawn after it's popped", n)
	}
	long := ts.Shape("a line of output that runs on well past the window's right edge, as a long log line does", typeset.FontSpec{Family: typeset.UI, Size: 13})
	if n := draw(func() { f.Glyphs(long, render.Point{X: 100, Y: 20}, white) }); n == 0 || n >= len(long.Glyphs)/2 {
		t.Errorf("a run mostly past the window's edge drew %d of its %d glyphs", n, len(long.Glyphs))
	}
}
