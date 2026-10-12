//go:build spike

package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/png"
	"os"
)

func loadPNG(name string) (image.Image, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

// compare writes mockup | native | diff side by side and prints how far apart they are.
func compare(mockup, native, out string) error {
	a, err := loadPNG(mockup)
	if err != nil {
		return err
	}
	b, err := loadPNG(native)
	if err != nil {
		return err
	}
	r := a.Bounds().Intersect(b.Bounds())
	w, h := r.Dx(), r.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, 3*w, h))
	draw.Draw(dst, image.Rect(0, 0, w, h), a, r.Min, draw.Src)
	draw.Draw(dst, image.Rect(w, 0, 2*w, h), b, r.Min, draw.Src)
	var sum float64
	var off int
	for y := range h {
		for x := range w {
			r1, g1, b1, _ := a.At(r.Min.X+x, r.Min.Y+y).RGBA()
			r2, g2, b2, _ := b.At(r.Min.X+x, r.Min.Y+y).RGBA()
			d := max(absDiff(r1, r2), absDiff(g1, g2), absDiff(b1, b2)) >> 8
			sum += float64(d)
			if d > 32 {
				off++
			}
			v := uint8(min(255, d*3))
			dst.Set(2*w+x, y, color.RGBA{v, v / 2, 0, 255})
		}
	}
	n := float64(w * h)
	fmt.Printf("%dx%d: mean abs diff %.2f/255, %.2f%% of pixels differ by more than 32/255\n", w, h, sum/n, 100*float64(off)/n)
	return writePNG(out, dst)
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}
