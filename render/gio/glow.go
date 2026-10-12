package gio

import (
	"image"

	"github.com/liamg/oriel/render/bake"

	"github.com/liamg/oriel/render"
	"image/color"
	"math"
)

// Glows stand in for CSS box-shadow blur, which Gio can't draw: a Gaussian-blurred
// box or disc is baked once per (size, blur, colour) and drawn as a stretched 9-slice.

type glowKey struct {
	w, h, pad int
	sigma     float32
	col       color.NRGBA
	disc      bool
}

// glow draws a `box-shadow: 0 0 blur col` around r.
func (c *Frame) glow(r render.Rect, blur float32, col color.NRGBA) {
	ir := c.irect(r)
	w, h := ir.Dx(), ir.Dy()
	sigma := blur * c.scale / 2
	pad := int(math.Ceil(float64(3 * sigma)))
	cw, ch := min(w, 2*pad+2), min(h, 2*pad+2)
	n := c.bake(glowKey{cw, ch, pad, sigma, col, false}, func() *nine { return newNine(bake.BoxGlow(w, h, cw, ch, pad, sigma, col)) })
	c.drawNine(n, ir.Inset(-pad), false)
}

// glowDisc draws a glow around a circle of radius r centred on (cx, cy).
func (c *Frame) glowDisc(cx, cy, r, blur float32, col color.NRGBA) {
	d := int(math.Round(float64(2 * r * c.scale)))
	sigma := blur * c.scale / 2
	pad := int(math.Ceil(float64(3 * sigma)))
	n := c.bake(glowKey{d, d, pad, sigma, col, true}, func() *nine { return &nine{pieces: [9]*image.NRGBA{bake.DiscGlow(d, pad, sigma, col)}} })
	x := int(math.Round(float64(cx*c.scale))) - d/2 - pad
	y := int(math.Round(float64(cy*c.scale))) - d/2 - pad
	at := image.Pt(x, y)
	c.piece(n, 0, at, image.Rectangle{Min: at, Max: at.Add(n.pieces[0].Bounds().Size())})
}
