package gio

import (
	"image"
	"image/color"
	"math"
)

// textGamma maps glyph coverage to the alpha written into the atlas so that Gio, which samples
// premultiplied sRGB textures and blends in linear light, shows what CSS's sRGB blending would:
// light text is solved over black, dark text over white. Dark text also gets stem darkening.
type textGamma struct {
	darken float64 // extra weight for dark text: coverage^(1/(1+darken))
	tables map[color.NRGBA]*[256]uint8
}

func (g *textGamma) alpha(c uint8, col color.NRGBA) uint8 {
	col.A = 255
	t, ok := g.tables[col]
	if !ok {
		if g.tables == nil || len(g.tables) > 1024 {
			g.tables = map[color.NRGBA]*[256]uint8{}
		}
		t = g.build(col)
		g.tables[col] = t
	}
	return t[c]
}

func (g *textGamma) build(col color.NRGBA) *[256]uint8 {
	var t [256]uint8
	light := luma(col) >= 0.5
	// The channel with the most contrast against the assumed backdrop.
	c, bg := float64(max(col.R, col.G, col.B))/255, 0.0
	if !light {
		c, bg = float64(min(col.R, col.G, col.B))/255, 1
	}
	for i := range 256 {
		a := float64(i) / 255
		if !light {
			a = math.Pow(a, 1/(1+g.darken))
		}
		switch i {
		case 0, 255:
			t[i] = uint8(i)
		default:
			t[i] = uint8(math.Round(255 * solveAlpha(c, bg, c*a+(1-a)*bg)))
		}
	}
	return &t
}

// solveAlpha finds a' with enc(lin(c·a') + (1-a')·lin(bg)) = want, by bisection.
func solveAlpha(c, bg, want float64) float64 {
	f := func(a float64) float64 { return enc(lin(c*a)+(1-a)*lin(bg)) - want }
	lo, hi := 0.0, 1.0
	rising := f(hi) >= f(lo)
	for range 40 {
		mid := (lo + hi) / 2
		if (f(mid) < 0) == rising {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

func lin(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func enc(v float64) float64 {
	if v <= 0.0031308 {
		return 12.92 * v
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

func luma(c color.NRGBA) float64 {
	return (0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)) / 255
}

// srgbAlpha returns a' with lin(a'·lin(c)) over black equal to sRGB a·c, for c's brightest channel.
func srgbAlpha(a, cmax float64) float64 {
	if cmax <= 0 {
		return a
	}
	return lin(a*cmax) / lin(cmax)
}

// solid remaps a translucent fill's alpha so Gio's linear-light blending over a dark backdrop
// matches CSS's sRGB blending (rendering.md, "Blending").
func solid(v color.NRGBA) color.NRGBA {
	if v.A == 255 || v.A == 0 {
		return v
	}
	m := float64(max(v.R, v.G, v.B)) / 255
	v.A = uint8(math.Round(255 * srgbAlpha(float64(v.A)/255, m)))
	return v
}

// tint writes cov in colour col into dst at p, as premultiplied sRGB.
func tint(dst *image.RGBA, p image.Point, cov *image.Alpha, col color.NRGBA, g *textGamma) {
	b := cov.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := cov.AlphaAt(x, y).A
			if c == 0 {
				continue
			}
			a := uint32(g.alpha(c, col)) * uint32(col.A) / 255
			i := dst.PixOffset(p.X+x-b.Min.X, p.Y+y-b.Min.Y)
			px := dst.Pix[i : i+4 : i+4]
			inv := 255 - a
			px[0] = uint8((uint32(col.R)*a + uint32(px[0])*inv) / 255)
			px[1] = uint8((uint32(col.G)*a + uint32(px[1])*inv) / 255)
			px[2] = uint8((uint32(col.B)*a + uint32(px[2])*inv) / 255)
			px[3] = uint8(a + uint32(px[3])*inv/255)
		}
	}
}

// premul writes an NRGBA image's pixel as premultiplied RGBA.
func premul(c color.NRGBA) (r, g, b, a uint8) {
	al := uint32(c.A)
	return uint8(uint32(c.R) * al / 255), uint8(uint32(c.G) * al / 255), uint8(uint32(c.B) * al / 255), c.A
}
