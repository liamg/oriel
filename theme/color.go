// Package theme resolves layered theme files (palette, semantic, component and syntax tokens) into the
// flat colour table widgets read each frame (theming.md). Colour maths follows CSS Color 4.
package theme

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"
)

// Color is non-premultiplied sRGB, as CSS writes it.
type Color = color.NRGBA

// OKLCH is a colour in OkLCh: L 0..1, C from 0, H in degrees (NaN when powerless), and alpha 0..1.
type OKLCH struct{ L, C, H, A float64 }

// Hex writes c as #rrggbb, or #rrggbbaa when it's translucent.
func Hex(c Color) string {
	if c.A == 255 {
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	return fmt.Sprintf("#%02x%02x%02x%02x", c.R, c.G, c.B, c.A)
}

// ParseColor reads a literal: #rgb, #rgba, #rrggbb, #rrggbbaa, rgb(), rgba() or oklch().
func ParseColor(s string) (Color, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "#"):
		return parseHex(s)
	case strings.HasPrefix(s, "oklch("):
		args, err := fnArgs(s, "oklch")
		if err != nil {
			return Color{}, err
		}
		if len(args) != 3 && len(args) != 4 {
			return Color{}, fmt.Errorf("oklch() takes L C H [/ alpha], not %q", s)
		}
		l, err := number(args[0], 1)
		if err != nil {
			return Color{}, err
		}
		c, err := number(args[1], 0.4) // 100% chroma is 0.4
		if err != nil {
			return Color{}, err
		}
		h, err := number(strings.TrimSuffix(args[2], "deg"), 0)
		if err != nil {
			return Color{}, err
		}
		a := 1.0
		if len(args) == 4 {
			if a, err = number(args[3], 1); err != nil {
				return Color{}, err
			}
		}
		return FromOKLCH(OKLCH{l, c, h, a}), nil
	case strings.HasPrefix(s, "rgb(") || strings.HasPrefix(s, "rgba("):
		name := "rgb"
		if strings.HasPrefix(s, "rgba(") {
			name = "rgba"
		}
		args, err := fnArgs(s, name)
		if err != nil {
			return Color{}, err
		}
		if len(args) != 3 && len(args) != 4 {
			return Color{}, fmt.Errorf("%s() takes three channels and an optional alpha, not %q", name, s)
		}
		var ch [4]float64
		ch[3] = 1
		for i, a := range args {
			scale := 255.0
			if i == 3 {
				scale = 1
			}
			v, err := number(a, scale)
			if err != nil {
				return Color{}, err
			}
			ch[i] = v
		}
		return rgba(byte8(ch[0]/255), byte8(ch[1]/255), byte8(ch[2]/255), byte8(ch[3])), nil
	}
	return Color{}, fmt.Errorf("not a colour: %q", s)
}

func parseHex(s string) (Color, error) {
	h := s[1:]
	if len(h) == 3 || len(h) == 4 {
		var b strings.Builder
		for _, r := range h {
			b.WriteRune(r)
			b.WriteRune(r)
		}
		h = b.String()
	}
	if len(h) == 6 {
		h += "ff"
	}
	if len(h) != 8 {
		return Color{}, fmt.Errorf("not a hex colour: %q", s)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return Color{}, fmt.Errorf("not a hex colour: %q", s)
	}
	return rgba(uint8(v>>24), uint8(v>>16), uint8(v>>8), uint8(v)), nil
}

// fnArgs splits name(a b c / d) or name(a, b, c, d) into its arguments.
func fnArgs(s, name string) ([]string, error) {
	if !strings.HasPrefix(s, name+"(") || !strings.HasSuffix(s, ")") {
		return nil, fmt.Errorf("not %s(): %q", name, s)
	}
	body := strings.NewReplacer(",", " ", "/", " ").Replace(s[len(name)+1 : len(s)-1])
	return strings.Fields(body), nil
}

// number reads a number, or a percentage of hundred.
func number(s string, hundred float64) (float64, error) {
	if p, ok := strings.CutSuffix(s, "%"); ok {
		v, err := strconv.ParseFloat(p, 64)
		return v / 100 * hundred, err
	}
	return strconv.ParseFloat(s, 64)
}

func byte8(v float64) uint8 { return uint8(math.Round(math.Max(0, math.Min(1, v)) * 255)) }

// ToOKLCH converts c (sRGB) to OkLCh.
func ToOKLCH(c Color) OKLCH {
	rgb := [3]float64{float64(c.R) / 255, float64(c.G) / 255, float64(c.B) / 255}
	lab := xyzToOKLab(linToXYZ(linSRGB(rgb)))
	return labToLCH(lab, float64(c.A)/255)
}

// FromOKLCH converts to sRGB, gamut mapped as CSS does.
func FromOKLCH(o OKLCH) Color {
	rgb := gamutMap(o)
	return rgba(byte8(rgb[0]), byte8(rgb[1]), byte8(rgb[2]), byte8(o.A))
}

func labToLCH(lab [3]float64, alpha float64) OKLCH {
	const epsilon = 0.000004
	h := math.Atan2(lab[2], lab[1]) * 180 / math.Pi
	c := math.Hypot(lab[1], lab[2])
	if h < 0 {
		h += 360
	}
	if c <= epsilon {
		h = math.NaN()
	}
	return OKLCH{lab[0], c, h, alpha}
}

func lchToLab(o OKLCH) [3]float64 {
	h := o.H
	if math.IsNaN(h) {
		h = 0
	}
	return [3]float64{o.L, o.C * math.Cos(h*math.Pi/180), o.C * math.Sin(h*math.Pi/180)}
}

func linSRGB(rgb [3]float64) [3]float64 {
	var out [3]float64
	for i, v := range rgb {
		sign, abs := 1.0, math.Abs(v)
		if v < 0 {
			sign = -1
		}
		if abs <= 0.04045 {
			out[i] = v / 12.92
		} else {
			out[i] = sign * math.Pow((abs+0.055)/1.055, 2.4)
		}
	}
	return out
}

func gamSRGB(rgb [3]float64) [3]float64 {
	var out [3]float64
	for i, v := range rgb {
		sign, abs := 1.0, math.Abs(v)
		if v < 0 {
			sign = -1
		}
		if abs > 0.0031308 {
			out[i] = sign * (1.055*math.Pow(abs, 1/2.4) - 0.055)
		} else {
			out[i] = 12.92 * v
		}
	}
	return out
}

func mul(m [3][3]float64, v [3]float64) [3]float64 {
	return [3]float64{
		m[0][0]*v[0] + m[0][1]*v[1] + m[0][2]*v[2],
		m[1][0]*v[0] + m[1][1]*v[1] + m[1][2]*v[2],
		m[2][0]*v[0] + m[2][1]*v[1] + m[2][2]*v[2],
	}
}

func linToXYZ(rgb [3]float64) [3]float64 {
	return mul([3][3]float64{
		{506752.0 / 1228815, 87881.0 / 245763, 12673.0 / 70218},
		{87098.0 / 409605, 175762.0 / 245763, 12673.0 / 175545},
		{7918.0 / 409605, 87881.0 / 737289, 1001167.0 / 1053270},
	}, rgb)
}

func xyzToLin(xyz [3]float64) [3]float64 {
	return mul([3][3]float64{
		{12831.0 / 3959, -329.0 / 214, -1974.0 / 3959},
		{-851781.0 / 878810, 1648619.0 / 878810, 36519.0 / 878810},
		{705.0 / 12673, -2585.0 / 12673, 705.0 / 667},
	}, xyz)
}

func xyzToOKLab(xyz [3]float64) [3]float64 {
	lms := mul([3][3]float64{
		{0.8190224379967030, 0.3619062600528904, -0.1288737815209879},
		{0.0329836539323885, 0.9292868615863434, 0.0361446663506424},
		{0.0481771893596242, 0.2642395317527308, 0.6335478284694309},
	}, xyz)
	for i := range lms {
		lms[i] = math.Cbrt(lms[i])
	}
	return mul([3][3]float64{
		{0.2104542683093140, 0.7936177747023054, -0.0040720430116193},
		{1.9779985324311684, -2.4285922420485799, 0.4505937096174110},
		{0.0259040424655478, 0.7827717124575296, -0.8086757549230774},
	}, lms)
}

func okLabToXYZ(lab [3]float64) [3]float64 {
	lms := mul([3][3]float64{
		{1.0000000000000000, 0.3963377773761749, 0.2158037573099136},
		{1.0000000000000000, -0.1055613458156586, -0.0638541728258133},
		{1.0000000000000000, -0.0894841775298119, -1.2914855480194092},
	}, lab)
	for i := range lms {
		lms[i] = lms[i] * lms[i] * lms[i]
	}
	return mul([3][3]float64{
		{1.2268798758459243, -0.5578149944602171, 0.2813910456659647},
		{-0.0405757452148008, 1.1122868032803170, -0.0717110580655164},
		{-0.0763729366746601, -0.4214933324022432, 1.5869240198367816},
	}, lms)
}

// lchToSRGB is gamma-encoded sRGB, possibly out of gamut.
func lchToSRGB(o OKLCH) [3]float64 { return gamSRGB(xyzToLin(okLabToXYZ(lchToLab(o)))) }

func inGamut(rgb [3]float64) bool {
	const eps = 0.000075
	for _, v := range rgb {
		if v < -eps || v > 1+eps {
			return false
		}
	}
	return true
}

func clip(rgb [3]float64) [3]float64 {
	for i := range rgb {
		rgb[i] = math.Max(0, math.Min(1, rgb[i]))
	}
	return rgb
}

func deltaEOK(a, b [3]float64) float64 {
	return math.Sqrt((a[0]-b[0])*(a[0]-b[0]) + (a[1]-b[1])*(a[1]-b[1]) + (a[2]-b[2])*(a[2]-b[2]))
}

func srgbToLab(rgb [3]float64) [3]float64 { return xyzToOKLab(linToXYZ(linSRGB(rgb))) }

// gamutMap is CSS Color 4's binary search gamut mapping with local MINDE, into sRGB.
func gamutMap(o OKLCH) [3]float64 {
	if o.L >= 1 {
		return [3]float64{1, 1, 1}
	}
	if o.L <= 0 {
		return [3]float64{0, 0, 0}
	}
	if rgb := lchToSRGB(o); inGamut(rgb) {
		return rgb
	}
	const jnd, epsilon = 0.02, 0.0001
	current := o
	clipped := clip(lchToSRGB(current))
	if deltaEOK(srgbToLab(clipped), lchToLab(current)) < jnd {
		return clipped
	}
	lo, hi := 0.0, o.C
	loInGamut := true
	for hi-lo > epsilon {
		current.C = (lo + hi) / 2
		rgb := lchToSRGB(current)
		if loInGamut && inGamut(rgb) {
			lo = current.C
			continue
		}
		clipped = clip(rgb)
		e := deltaEOK(srgbToLab(clipped), lchToLab(current))
		if e < jnd {
			if jnd-e < epsilon {
				return clipped
			}
			loInGamut = false
			lo = current.C
		} else {
			hi = current.C
		}
	}
	return clipped
}

// Mix is color-mix(in oklch, a, b p): p of b into a, along the shorter hue arc, with premultiplied alpha.
func Mix(a, b Color, p float64) Color {
	x, y := ToOKLCH(a), ToOKLCH(b)
	return FromOKLCH(MixOKLCH(x, y, p))
}

func MixOKLCH(x, y OKLCH, p float64) OKLCH {
	// A powerless hue takes the other colour's.
	switch {
	case math.IsNaN(x.H) && math.IsNaN(y.H):
		x.H, y.H = 0, 0
	case math.IsNaN(x.H):
		x.H = y.H
	case math.IsNaN(y.H):
		y.H = x.H
	}
	if d := y.H - x.H; d > 180 {
		x.H += 360
	} else if d < -180 {
		y.H += 360
	}
	alpha := x.A*(1-p) + y.A*p
	lerp := func(u, v float64) float64 { return u*(1-p) + v*p }
	out := OKLCH{H: math.Mod(lerp(x.H, y.H), 360), A: alpha}
	// Premultiply L and C (not hue), interpolate, then divide by the interpolated alpha.
	if alpha > 0 {
		out.L = lerp(x.L*x.A, y.L*y.A) / alpha
		out.C = lerp(x.C*x.A, y.C*y.A) / alpha
	}
	return out
}

// Lighten moves c's OkLCh lightness by d (0..1); negative darkens.
func Lighten(c Color, d float64) Color {
	o := ToOKLCH(c)
	o.L = math.Max(0, math.Min(1, o.L+d))
	return FromOKLCH(o)
}

// WithAlpha sets c's alpha to a (0..1).
func WithAlpha(c Color, a float64) Color {
	c.A = byte8(a)
	return c
}

// Over composites fg on an opaque bg as CSS does, in sRGB, and returns the opaque result.
func Over(fg, bg Color) Color {
	a := float64(fg.A) / 255
	ch := func(f, b uint8) uint8 { return byte8((float64(f)*a + float64(b)*(1-a)) / 255) }
	return rgba(ch(fg.R, bg.R), ch(fg.G, bg.G), ch(fg.B, bg.B), 255)
}

func rgba(r, g, b, a uint8) Color { return Color{R: r, G: g, B: b, A: a} }
