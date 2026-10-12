package scene

import (
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// Glows stand in for CSS box-shadow blur, which Gio can't draw: a Gaussian-blurred
// box or disc is baked once per (size, blur, colour) and drawn as a stretched 9-slice.

type glowKey struct {
	w, h, pad int
	sigma     float32
	col       color.NRGBA
	disc      bool
}

func phi(x float64) float64 { return 0.5 * (1 + math.Erf(x/math.Sqrt2)) }

// Glow draws a `box-shadow: 0 0 blur col` around r.
func (c *Canvas) Glow(r Rect, blur float32, col color.NRGBA) {
	ir := c.irect(r)
	w, h := ir.Dx(), ir.Dy()
	sigma := blur * c.Scale / 2
	pad := int(math.Ceil(float64(3 * sigma)))
	cw, ch := min(w, 2*pad+2), min(h, 2*pad+2)
	k := glowKey{cw, ch, pad, sigma, col, false}
	img, ok := c.glows[k]
	if !ok {
		img = paint.NewImageOp(boxGlow(w, h, cw, ch, pad, sigma, col))
		c.glows[k] = img
	}
	c.nineSlice(img, cw+2*pad, ch+2*pad, ir.Inset(-pad), false)
}

// GlowDisc draws a glow around a circle of radius r centred on (cx, cy).
func (c *Canvas) GlowDisc(cx, cy, r, blur float32, col color.NRGBA) {
	d := int(math.Round(float64(2 * r * c.Scale)))
	sigma := blur * c.Scale / 2
	pad := int(math.Ceil(float64(3 * sigma)))
	k := glowKey{d, d, pad, sigma, col, true}
	img, ok := c.glows[k]
	if !ok {
		img = paint.NewImageOp(discGlow(d, pad, sigma, col))
		c.glows[k] = img
	}
	x := int(math.Round(float64(cx*c.Scale))) - d/2 - pad
	y := int(math.Round(float64(cy*c.Scale))) - d/2 - pad
	defer op.Offset(image.Pt(x, y)).Push(c.Ops).Pop()
	img.Add(c.Ops)
	paint.PaintOp{}.Add(c.Ops)
}

// boxGlow bakes the blurred box. When the box is wider (taller) than cw (ch), only its
// edges are baked and the middle column (row) is stretched.
func boxGlow(w, h, cw, ch, pad int, sigma float32, col color.NRGBA) *image.NRGBA {
	prof := func(n, core int) []float64 {
		out := make([]float64, core+2*pad)
		for i := range out {
			x := float64(i) + 0.5
			if i >= len(out)/2 {
				x = float64(len(out)-i) - 0.5
			}
			s := float64(sigma)
			out[i] = phi((x-float64(pad))/s) - phi((x-float64(pad+n))/s)
		}
		return out
	}
	px, py := prof(w, cw), prof(h, ch)
	img := image.NewNRGBA(image.Rect(0, 0, len(px), len(py)))
	for y, vy := range py {
		for x, vx := range px {
			img.SetNRGBA(x, y, glowPixel(col, vx*vy))
		}
	}
	return img
}

func discGlow(d, pad int, sigma float32, col color.NRGBA) *image.NRGBA {
	n := d + 2*pad
	r := float64(d) / 2
	c := float64(n) / 2
	// Disc coverage, supersampled 4×4.
	cov := make([]float64, n*n)
	for y := range n {
		for x := range n {
			var in float64
			for sy := range 4 {
				for sx := range 4 {
					dx := float64(x) + (float64(sx)+0.5)/4 - c
					dy := float64(y) + (float64(sy)+0.5)/4 - c
					if dx*dx+dy*dy <= r*r {
						in++
					}
				}
			}
			cov[y*n+x] = in / 16
		}
	}
	// Separable Gaussian blur.
	k := make([]float64, 2*pad+1)
	var sum float64
	for i := range k {
		x := float64(i - pad)
		k[i] = math.Exp(-x * x / (2 * float64(sigma*sigma)))
		sum += k[i]
	}
	for i := range k {
		k[i] /= sum
	}
	tmp := make([]float64, n*n)
	for y := range n {
		for x := range n {
			var v float64
			for i, kv := range k {
				if xx := x + i - pad; xx >= 0 && xx < n {
					v += cov[y*n+xx] * kv
				}
			}
			tmp[y*n+x] = v
		}
	}
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := range n {
		for x := range n {
			var v float64
			for i, kv := range k {
				if yy := y + i - pad; yy >= 0 && yy < n {
					v += tmp[yy*n+x] * kv
				}
			}
			img.SetNRGBA(x, y, glowPixel(col, v))
		}
	}
	return img
}

// nineSlice draws a tw×th texture into dst, stretching its middle row and column.
// hollow skips the middle piece, for borders.
func (c *Canvas) nineSlice(img paint.ImageOp, tw, th int, dst image.Rectangle, hollow bool) {
	xs := splits(tw, dst.Min.X, dst.Dx())
	ys := splits(th, dst.Min.Y, dst.Dy())
	for j, sy := range ys {
		for i, sx := range xs {
			if sx.dn <= 0 || sy.dn <= 0 || hollow && i == 1 && j == 1 {
				continue
			}
			cl := clip.Rect(image.Rect(sx.d0, sy.d0, sx.d0+sx.dn, sy.d0+sy.dn)).Push(c.Ops)
			scale := f32.Pt(float32(sx.dn)/float32(sx.sn), float32(sy.dn)/float32(sy.sn))
			tr := op.Affine(f32.AffineId().Scale(f32.Pt(0, 0), scale).Offset(f32.Pt(float32(sx.d0)-float32(sx.s0)*scale.X, float32(sy.d0)-float32(sy.s0)*scale.Y))).Push(c.Ops)
			img.Add(c.Ops)
			paint.PaintOp{}.Add(c.Ops)
			tr.Pop()
			cl.Pop()
		}
	}
}

type span struct{ s0, sn, d0, dn int }

func splits(tn, d0, dn int) []span {
	if tn == dn {
		return []span{{0, tn, d0, dn}}
	}
	m := tn / 2
	right := tn - m - 1
	return []span{{0, m, d0, m}, {m, 1, d0 + m, dn - m - right}, {m + 1, right, d0 + dn - right, right}}
}

// glowPixel is col at coverage cov. Gio samples images as premultiplied sRGB, which already
// blends like CSS, so unlike solid fills the alpha needs no remapping.
func glowPixel(col color.NRGBA, cov float64) color.NRGBA {
	a := cov * float64(col.A) / 255
	return color.NRGBA{col.R, col.G, col.B, uint8(math.Min(255, math.Round(a*255)))}
}
