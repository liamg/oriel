package gio

import (
	"image"
	"image/color"
	"image/draw"
)

// Diff is how far a render is from a reference image.
type Diff struct {
	Mean float64 // mean of the largest channel difference per pixel, out of 255
	Off  float64 // fraction of pixels whose largest channel differs by more than 32
}

// Compare measures want against got over the area they share, and returns them side by side
// with a heat map of the difference: want | got | diff.
func Compare(want, got image.Image) (Diff, *image.RGBA) {
	r := want.Bounds().Intersect(got.Bounds())
	w, h := r.Dx(), r.Dy()
	out := image.NewRGBA(image.Rect(0, 0, 3*w, h))
	draw.Draw(out, image.Rect(0, 0, w, h), want, r.Min, draw.Src)
	draw.Draw(out, image.Rect(w, 0, 2*w, h), got, r.Min, draw.Src)
	var sum float64
	var off int
	for y := range h {
		for x := range w {
			r1, g1, b1, _ := want.At(r.Min.X+x, r.Min.Y+y).RGBA()
			r2, g2, b2, _ := got.At(r.Min.X+x, r.Min.Y+y).RGBA()
			d := max(absDiff(r1, r2), absDiff(g1, g2), absDiff(b1, b2)) >> 8
			sum += float64(d)
			if d > 32 {
				off++
			}
			v := uint8(min(255, d*3))
			out.Set(2*w+x, y, color.RGBA{v, v / 2, 0, 255})
		}
	}
	n := float64(w * h)
	return Diff{Mean: sum / n, Off: float64(off) / n}, out
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}
