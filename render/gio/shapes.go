package gio

import (
	"image"
	"image/color"

	"github.com/liamg/oriel/render"
)

type bakedKey struct {
	r      int     // corner radius, device px
	w      float32 // border width, device px; 0 fills
	col    color.NRGBA
	nx, ny int // image size
}

// bakeRRect returns an nx×ny image of a rounded rect with radius r. Sides of 2r+3 leave a
// middle row or column to stretch; a side shorter than that is drawn at its real size.
// w > 0 draws only a border of that width.
func bakeRRect(r int, w float32, col color.NRGBA, nx, ny int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, nx, ny))
	fx, fy := float32(nx), float32(ny)
	inside := func(x, y, x0, y0, x1, y1, rad float32) bool {
		if x < x0 || y < y0 || x > x1 || y > y1 {
			return false
		}
		cx, cy := min(max(x, x0+rad), x1-rad), min(max(y, y0+rad), y1-rad)
		dx, dy := x-cx, y-cy
		return dx*dx+dy*dy <= rad*rad
	}
	rf := min(float32(r), fx/2, fy/2)
	const ss = 4
	for y := range ny {
		for x := range nx {
			var in int
			for sy := range ss {
				for sx := range ss {
					px := float32(x) + (float32(sx)+0.5)/ss
					py := float32(y) + (float32(sy)+0.5)/ss
					if !inside(px, py, 0, 0, fx, fy, rf) {
						continue
					}
					if w > 0 && inside(px, py, w, w, fx-w, fy-w, max(rf-w, 0)) {
						continue
					}
					in++
				}
			}
			img.SetNRGBA(x, y, glowPixel(col, float64(in)/(ss*ss)))
		}
	}
	return img
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
	nn := c.bake(bakedKey{r: rp, w: w, col: col, nx: nx, ny: ny}, func() *nine { return newNine(bakeRRect(rp, w, col, nx, ny)) })
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
