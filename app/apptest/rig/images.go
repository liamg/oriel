package rig

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"math"
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

// PNGAtDPI is img encoded as a PNG whose pHYs chunk says dpi.
func PNGAtDPI(t *testing.T, img image.Image, dpi float64) []byte {
	t.Helper()
	b := PNG(t, img)
	ppm := uint32(math.Round(dpi / 0.0254))
	data := append(binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, ppm), ppm), 1)
	chunk := append(binary.BigEndian.AppendUint32(nil, uint32(len(data))), "pHYs"...)
	chunk = append(chunk, data...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	ihdr := 8 + 12 + 13
	return bytes.Join([][]byte{b[:ihdr], chunk, b[ihdr:]}, nil)
}
