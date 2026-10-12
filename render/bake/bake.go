// Package bake draws the shapes a GPU backend can't, or shouldn't, every frame: Gaussian glows and
// antialiased rounded rects, as images. render/gio slices them into 9-slices; render/raster draws them.
package bake

import (
	"image"
	"image/color"
	"math"
)

func Phi(x float64) float64 { return 0.5 * (1 + math.Erf(x/math.Sqrt2)) }

// BoxGlow bakes the blurred box. When the box is wider (taller) than cw (ch), only its
// edges are baked and the middle column (row) is stretched.
func BoxGlow(w, h, cw, ch, pad int, sigma float32, col color.NRGBA) *image.NRGBA {
	prof := func(n, core int) []float64 {
		out := make([]float64, core+2*pad)
		for i := range out {
			x := float64(i) + 0.5
			if i >= len(out)/2 {
				x = float64(len(out)-i) - 0.5
			}
			s := float64(sigma)
			out[i] = Phi((x-float64(pad))/s) - Phi((x-float64(pad+n))/s)
		}
		return out
	}
	px, py := prof(w, cw), prof(h, ch)
	img := image.NewNRGBA(image.Rect(0, 0, len(px), len(py)))
	for y, vy := range py {
		for x, vx := range px {
			img.SetNRGBA(x, y, GlowPixel(col, vx*vy))
		}
	}
	return img
}

func DiscGlow(d, pad int, sigma float32, col color.NRGBA) *image.NRGBA {
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
			img.SetNRGBA(x, y, GlowPixel(col, v))
		}
	}
	return img
}

// GlowPixel is col at coverage cov. Gio samples images as premultiplied sRGB, which already
// blends like CSS, so unlike solid fills the alpha needs no remapping.
func GlowPixel(col color.NRGBA, cov float64) color.NRGBA {
	a := cov * float64(col.A) / 255
	return color.NRGBA{col.R, col.G, col.B, uint8(math.Min(255, math.Round(a*255)))}
}

// RRect returns an nx×ny image of a rounded rect with radius r. Sides of 2r+3 leave a
// middle row or column to stretch; a side shorter than that is drawn at its real size.
// w > 0 draws only a border of that width.
func RRect(r int, w float32, col color.NRGBA, nx, ny int) *image.NRGBA {
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
			img.SetNRGBA(x, y, GlowPixel(col, float64(in)/(ss*ss)))
		}
	}
	return img
}
