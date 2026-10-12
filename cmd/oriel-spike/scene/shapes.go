package scene

import (
	"image"
	"image/color"

	"gioui.org/op/paint"
)

type bakedKey struct {
	r   int     // corner radius, device px
	w   float32 // border width, device px; 0 fills
	col color.NRGBA
}

// bakeRRect returns an n×n image of a rounded rect with radius r, n = 2r+3, so the middle
// row and column can be stretched. w > 0 draws only a border of that width.
func bakeRRect(r int, w float32, col color.NRGBA) (*image.NRGBA, int) {
	n := 2*r + 3
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	fn := float32(n)
	inside := func(x, y, x0, y0, x1, y1, rad float32) bool {
		if x < x0 || y < y0 || x > x1 || y > y1 {
			return false
		}
		cx, cy := min(max(x, x0+rad), x1-rad), min(max(y, y0+rad), y1-rad)
		dx, dy := x-cx, y-cy
		return dx*dx+dy*dy <= rad*rad
	}
	rf := float32(r)
	const ss = 4
	for y := range n {
		for x := range n {
			var in int
			for sy := range ss {
				for sx := range ss {
					px := float32(x) + (float32(sx)+0.5)/ss
					py := float32(y) + (float32(sy)+0.5)/ss
					if !inside(px, py, 0, 0, fn, fn, rf) {
						continue
					}
					if w > 0 && inside(px, py, w, w, fn-w, fn-w, max(rf-w, 0)) {
						continue
					}
					in++
				}
			}
			img.SetNRGBA(x, y, glowPixel(col, float64(in)/(ss*ss)))
		}
	}
	return img, n
}

// bakedShape draws a rounded rect, or its border when width > 0, as a 9-slice of a baked
// image instead of a stencilled path. It reports false when r is too small to slice.
func (c *Canvas) bakedShape(r Rect, rad, width float32, col color.NRGBA) bool {
	ir := c.irect(r)
	rp := c.px(min(rad, r.W/2, r.H/2))
	w := float32(0)
	if width > 0 {
		w = float32(c.px(width))
		if w < 1 {
			w = width * c.Scale
		}
	}
	n := 2*rp + 3
	if ir.Dx() < n || ir.Dy() < n {
		return false
	}
	k := bakedKey{r: rp, w: w, col: col}
	img, ok := c.shapes[k]
	if !ok {
		src, _ := bakeRRect(rp, w, col)
		img = paint.NewImageOp(src)
		img.Filter = paint.FilterNearest
		c.shapes[k] = img
	}
	c.nineSlice(img, n, n, ir, w > 0)
	return true
}
