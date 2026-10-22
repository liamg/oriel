package rig

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// Photo is a w×h picture: a sky over a field with a sun, so a screenshot of it reads as one.
func Photo(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.NRGBA{R: uint8(60 + 80*y/h), G: uint8(120 + 60*y/h), B: 230, A: 255}
			if y > h*2/3 {
				c = color.NRGBA{R: 50, G: uint8(140 + 40*x/w), B: 60, A: 255}
			}
			if dx, dy := x-w*3/4, y-h/4; dx*dx+dy*dy < (h/8)*(h/8) {
				c = color.NRGBA{R: 255, G: 210, B: 70, A: 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// PNG is img encoded as a PNG.
func PNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
