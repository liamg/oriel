package gio

import (
	"image/color"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/bake"
)

type bakedKey struct {
	r      int     // corner radius, device px
	w      float32 // border width, device px; 0 fills
	col    color.NRGBA
	nx, ny int // image size
}

// bakedShape draws a rounded rect, or its border when width > 0, as a 9-slice of a baked
// image instead of a stencilled path.
func (c *Frame) bakedShape(r render.Rect, rad, width float32, col color.NRGBA) {
	ir := c.irect(r)
	rp := c.px(min(rad, r.Dx()/2, r.Dy()/2))
	w := float32(0)
	if width > 0 {
		w = float32(c.px(width))
		if w < 1 {
			w = width * c.scale
		}
	}
	if ir.Empty() {
		return
	}
	n := 2*rp + 3
	nx, ny := min(n, ir.Dx()), min(n, ir.Dy())
	nn := c.bake(bakedKey{r: rp, w: w, col: col, nx: nx, ny: ny}, func() *nine { return newNine(bake.RRect(rp, w, col, nx, ny)) })
	c.drawNine(nn, ir, w > 0 && nx == n && ny == n)
}

// bake returns the baked 9-slice for k, making it the first time.
func (c *Frame) bake(k any, make func() *nine) *nine {
	b, ok := c.baked[k]
	if !ok {
		b = &bakedEntry{n: make()}
		c.baked[k] = b
	}
	b.used = c.frame
	return b.n
}
