package scene

import (
	"encoding/binary"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	"math"

	"gioui.org/op"
	"gioui.org/op/paint"
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
func (c *Canvas) bakedShape(r Rect, rad, width float32, col color.NRGBA) {
	ir := c.irect(r)
	rp := c.px(min(rad, r.W/2, r.H/2))
	w := float32(0)
	if width > 0 {
		w = float32(c.px(width))
		if w < 1 {
			w = width * c.Scale
		}
	}
	if ir.Empty() {
		return
	}
	n := 2*rp + 3
	nx, ny := min(n, ir.Dx()), min(n, ir.Dy())
	k := bakedKey{r: rp, w: w, col: col, nx: nx, ny: ny}
	nn, ok := c.shapes[k]
	if !ok {
		nn = newNine(bakeRRect(rp, w, col, nx, ny))
		c.shapes[k] = nn
	}
	c.drawNine(nn, ir, w > 0 && nx == n && ny == n)
}

type barsImage struct {
	op   paint.ImageOp
	at   image.Point
	used int
}

// Bars draws a bar chart: bar i is w wide at x+i·step, vals[i]·maxH tall, standing on bottom.
// With BakedShapes the chart is one image, redrawn only when vals change.
func (c *Canvas) Bars(x, bottom, step, w, maxH float32, vals []float32, col color.NRGBA) {
	if !c.BakedShapes {
		for i, v := range vals {
			c.Fill(Rect{x + float32(i)*step, bottom - v*maxH, w, v * maxH}, col)
		}
		return
	}
	h := fnv.New64a()
	var b [4]byte
	for _, f := range []float32{x, bottom, step, w, maxH, c.Scale} {
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(f))
		h.Write(b[:])
	}
	for _, v := range vals {
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(v))
		h.Write(b[:])
	}
	h.Write([]byte{col.R, col.G, col.B, col.A})
	k := h.Sum64()
	bi, ok := c.bars[k]
	if !ok {
		area := c.irect(Rect{x, bottom - maxH, float32(len(vals)) * step, maxH})
		img := image.NewNRGBA(image.Rectangle{Max: area.Size()})
		for i, v := range vals {
			r := c.irect(Rect{x + float32(i)*step, bottom - v*maxH, w, v * maxH}).Sub(area.Min).Intersect(img.Rect)
			draw.Draw(img, r, image.NewUniform(col), image.Point{}, draw.Src)
		}
		bi = &barsImage{op: paint.NewImageOp(img), at: area.Min}
		bi.op.Filter = paint.FilterNearest
		if c.bars == nil {
			c.bars = map[uint64]*barsImage{}
		}
		c.bars[k] = bi
	}
	bi.used = c.frame
	defer op.Offset(bi.at).Push(c.Ops).Pop()
	bi.op.Add(c.Ops)
	paint.PaintOp{}.Add(c.Ops)
}
