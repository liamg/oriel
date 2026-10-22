package gio

import (
	"bytes"
	"testing"

	"github.com/liamg/oriel/render"
)

func star(at render.Point) *render.Path {
	var p render.Path
	p.MoveTo(render.Point{X: at.X + 10, Y: at.Y}).LineTo(render.Point{X: at.X + 20, Y: at.Y + 20}).
		QuadTo(render.Point{X: at.X + 10, Y: at.Y + 25}, render.Point{X: at.X, Y: at.Y + 20}).Close()
	return &p
}

// A path drawn again, moved by whole pixels, is drawn from the recording it had, and looks as one drawn afresh there.
func TestPathsAreRecordedOnce(t *testing.T) {
	style := render.Style{Fill: render.Color{R: 200, G: 80, A: 255}, Stroke: render.Color{B: 220, A: 255}, Width: 1.5}
	draw := func(hl *Headless, at render.Point) {
		t.Helper()
		if err := hl.Draw(func(f render.Frame) {
			f.Rect(render.Rect{Max: render.Point{X: 64, Y: 64}}, render.Style{Fill: render.Color{A: 255}})
			f.Path(star(at), style)
		}); err != nil {
			t.Fatal(err)
		}
	}
	hl, err := NewHeadless(64, 64, 2)
	if err != nil {
		t.Skipf("no offscreen GPU surface: %v", err)
	}
	defer hl.Release()
	draw(hl, render.Point{X: 4, Y: 4})
	first := map[*shapeEntry]bool{}
	for _, e := range hl.frame.shapes {
		first[e] = true
	}
	draw(hl, render.Point{X: 30, Y: 21})
	if len(hl.frame.shapes) != 2 {
		t.Fatalf("%d recordings for a fill and a stroke moved, want 2", len(hl.frame.shapes))
	}
	for _, e := range hl.frame.shapes {
		if !first[e] {
			t.Fatal("a moved path was recorded again")
		}
	}
	moved, err := hl.Screenshot()
	if err != nil {
		t.Fatal(err)
	}

	fresh, err := NewHeadless(64, 64, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Release()
	draw(fresh, render.Point{X: 30, Y: 21})
	want, err := fresh.Screenshot()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(moved.Pix, want.Pix) {
		t.Fatal("the moved path draws differently from one drawn there afresh")
	}
}
