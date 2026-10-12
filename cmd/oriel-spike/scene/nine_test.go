package scene

import (
	"image"
	"image/color"
	"testing"

	"gioui.org/op"
)

func TestNinePieces(t *testing.T) {
	src := bakeRRect(3, 0, color.NRGBA{200, 100, 50, 255}, 9, 9)
	n := newNine(src)
	for i, want := range []image.Point{{9, 9}, {4, 4}, {4, 4}, {4, 4}, {4, 4}, {nineTile, 4}, {nineTile, 4}, {4, nineTile}, {4, nineTile}} {
		if got := n.pieces[i].Bounds().Size(); got != want {
			t.Errorf("piece %d is %v, want %v", i, got, want)
		}
	}
	if n.centre != (color.NRGBA{200, 100, 50, 255}) {
		t.Errorf("centre %v", n.centre)
	}
	// Every size, including smaller than the image and longer than a tile, draws without panicking.
	var ops op.Ops
	c := NewCanvas(&ops, nil, 1)
	c.Begin(&ops, 1)
	for _, r := range []image.Rectangle{image.Rect(0, 0, 5, 5), image.Rect(0, 0, 9, 9), image.Rect(3, 3, 900, 12), image.Rect(0, 0, 9, 600)} {
		c.drawNine(n, r, false)
		c.drawNine(n, r, true)
	}
	if c.atlas.Len() != 9 {
		t.Errorf("atlas holds %d pieces, want the 9 drawn", c.atlas.Len())
	}
}

func TestBakedThinPill(t *testing.T) {
	// A 4 px pill with a 2 px radius bakes at its real height rather than falling back to a path.
	img := bakeRRect(2, 0, color.NRGBA{255, 255, 255, 255}, 7, 4)
	if img.Bounds() != image.Rect(0, 0, 7, 4) {
		t.Fatalf("bounds %v", img.Bounds())
	}
	if a := img.NRGBAAt(3, 2).A; a != 255 {
		t.Errorf("middle alpha %d, want 255", a)
	}
	if a := img.NRGBAAt(0, 0).A; a > 128 {
		t.Errorf("corner alpha %d, want mostly transparent", a)
	}
}
