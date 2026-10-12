// Package scene draws the spike's scenes with Gio ops, in CSS pixels scaled to the device.
package scene

import (
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"golang.org/x/image/math/fixed"
)

// Canvas draws in CSS pixels; Scale maps them to device pixels.
type Canvas struct {
	Ops    *op.Ops
	Shaper *text.Shaper
	Scale  float32

	// SRGBBlend remaps translucent alphas so Gio's linear-light blending over a dark
	// backdrop matches CSS's sRGB blending.
	SRGBBlend bool

	// Mode picks the text path: Gio's vector glyphs, or the glyph atlas.
	Mode TextMode
	// BakedShapes draws rounded rects and borders as baked 9-slices instead of paths.
	BakedShapes bool

	glyphs  []text.Glyph
	metrics map[metricKey]metric
	glows   map[glowKey]paint.ImageOp
	shapes  map[bakedKey]paint.ImageOp
	ts      *typesetter
}

// NewCanvas returns a canvas drawing into ops.
func NewCanvas(ops *op.Ops, sh *text.Shaper, scale float32) *Canvas {
	return &Canvas{Ops: ops, Shaper: sh, Scale: scale, SRGBBlend: true, metrics: map[metricKey]metric{}, glows: map[glowKey]paint.ImageOp{}, shapes: map[bakedKey]paint.ImageOp{}}
}

// Begin starts a frame into ops, keeping the caches (glows, metrics, glyphs) from earlier frames.
func (c *Canvas) Begin(ops *op.Ops, scale float32) {
	if scale != c.Scale {
		c.metrics = map[metricKey]metric{}
		c.glows = map[glowKey]paint.ImageOp{}
		c.shapes = map[bakedKey]paint.ImageOp{}
	}
	c.Ops, c.Scale = ops, scale
	if c.ts != nil {
		c.ts.beginFrame()
	}
}

// srgbAlpha returns a' with lin(a'·lin(c)) over black equal to sRGB a·c, for c's brightest channel.
func srgbAlpha(a, cmax float64) float64 {
	if cmax <= 0 {
		return a
	}
	return lin(a*cmax) / lin(cmax)
}

func lin(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

// col applies SRGBBlend to a colour about to be painted.
func (c *Canvas) col(v color.NRGBA) color.NRGBA {
	if !c.SRGBBlend || v.A == 255 || v.A == 0 {
		return v
	}
	m := float64(max(v.R, v.G, v.B)) / 255
	v.A = uint8(math.Round(255 * srgbAlpha(float64(v.A)/255, m)))
	return v
}

// Rect is a rectangle in CSS pixels.
type Rect struct{ X, Y, W, H float32 }

func (r Rect) inset(d float32) Rect { return Rect{r.X + d, r.Y + d, r.W - 2*d, r.H - 2*d} }

func (c *Canvas) px(v float32) int { return int(math.Round(float64(v * c.Scale))) }

func (c *Canvas) irect(r Rect) image.Rectangle {
	return image.Rect(c.px(r.X), c.px(r.Y), c.px(r.X+r.W), c.px(r.Y+r.H))
}

// Fill paints r, snapped to device pixels.
func (c *Canvas) Fill(r Rect, col color.NRGBA) {
	paint.FillShape(c.Ops, c.col(col), clip.Rect(c.irect(r)).Op())
}

// RRect paints a rounded rectangle.
func (c *Canvas) RRect(r Rect, rad float32, col color.NRGBA) {
	if c.BakedShapes && c.bakedShape(r, rad, 0, col) {
		return
	}
	rad = min(rad, r.W/2, r.H/2)
	paint.FillShape(c.Ops, c.col(col), clip.UniformRRect(c.irect(r), c.px(rad)).Op(c.Ops))
}

// Border strokes the inside of r, like a CSS border.
func (c *Canvas) Border(r Rect, rad, width float32, col color.NRGBA) {
	if c.BakedShapes && c.bakedShape(r, rad, width, col) {
		return
	}
	rad = min(rad, r.W/2, r.H/2)
	ir := c.irect(r)
	w := float32(c.px(width))
	if w < 1 {
		w = width * c.Scale
	}
	x0, y0 := float32(ir.Min.X)+w/2, float32(ir.Min.Y)+w/2
	x1, y1 := float32(ir.Max.X)-w/2, float32(ir.Max.Y)-w/2
	rr := max(rad*c.Scale-w/2, 0)
	paint.FillShape(c.Ops, c.col(col), clip.Stroke{Path: rrectPath(c.Ops, x0, y0, x1, y1, rr), Width: w}.Op())
}

// rrectPath outlines a rounded rectangle with float coordinates.
func rrectPath(ops *op.Ops, x0, y0, x1, y1, r float32) clip.PathSpec {
	const k = 0.5523 // circle approximation with cubic Béziers
	var p clip.Path
	p.Begin(ops)
	p.MoveTo(f32.Pt(x0+r, y0))
	p.LineTo(f32.Pt(x1-r, y0))
	p.CubeTo(f32.Pt(x1-r+r*k, y0), f32.Pt(x1, y0+r-r*k), f32.Pt(x1, y0+r))
	p.LineTo(f32.Pt(x1, y1-r))
	p.CubeTo(f32.Pt(x1, y1-r+r*k), f32.Pt(x1-r+r*k, y1), f32.Pt(x1-r, y1))
	p.LineTo(f32.Pt(x0+r, y1))
	p.CubeTo(f32.Pt(x0+r-r*k, y1), f32.Pt(x0, y1-r+r*k), f32.Pt(x0, y1-r))
	p.LineTo(f32.Pt(x0, y0+r))
	p.CubeTo(f32.Pt(x0, y0+r-r*k), f32.Pt(x0+r-r*k, y0), f32.Pt(x0+r, y0))
	p.Close()
	return p.End()
}

// Line strokes a straight line of the given width, unsnapped, so a 0.5 px hairline stays 0.5 px.
func (c *Canvas) Line(x0, y0, x1, y1, width float32, col color.NRGBA) {
	s := c.Scale
	var p clip.Path
	p.Begin(c.Ops)
	p.MoveTo(f32.Pt(x0*s, y0*s))
	p.LineTo(f32.Pt(x1*s, y1*s))
	paint.FillShape(c.Ops, c.col(col), clip.Stroke{Path: p.End(), Width: width * s}.Op())
}

// Wavy draws a squiggle underline from x0 to x1 at y.
func (c *Canvas) Wavy(x0, x1, y float32, col color.NRGBA) {
	s := c.Scale
	var p clip.Path
	p.Begin(c.Ops)
	p.MoveTo(f32.Pt(x0*s, y*s))
	const half = 2.0
	for x, up := x0, true; x < x1; x, up = x+half, !up {
		dy := float32(1.5)
		if up {
			dy = -dy
		}
		p.QuadTo(f32.Pt((x+half/2)*s, (y+dy)*s), f32.Pt((x+half)*s, y*s))
	}
	paint.FillShape(c.Ops, c.col(col), clip.Stroke{Path: p.End(), Width: s}.Op())
}

// Ring strokes a circle.
func (c *Canvas) Ring(cx, cy, r, width float32, col color.NRGBA) {
	s := c.Scale
	e := clip.Ellipse{Min: image.Pt(int((cx-r)*s), int((cy-r)*s)), Max: image.Pt(int((cx+r)*s), int((cy+r)*s))}
	paint.FillShape(c.Ops, c.col(col), clip.Stroke{Path: e.Path(c.Ops), Width: width * s}.Op())
}

// Disc fills a circle.
func (c *Canvas) Disc(cx, cy, r float32, col color.NRGBA) {
	s := c.Scale
	e := clip.Ellipse{Min: image.Pt(int(math.Round(float64((cx-r)*s))), int(math.Round(float64((cy-r)*s)))), Max: image.Pt(int(math.Round(float64((cx+r)*s))), int(math.Round(float64((cy+r)*s))))}
	paint.FillShape(c.Ops, c.col(col), e.Op(c.Ops))
}

// Gradient fills r with a two-stop linear gradient, horizontal or vertical.
func (c *Canvas) Gradient(r Rect, horizontal bool, from, to color.NRGBA) {
	ir := c.irect(r)
	g := paint.LinearGradientOp{Color1: c.col(from), Color2: c.col(to), Stop1: f32.Pt(float32(ir.Min.X), float32(ir.Min.Y))}
	if horizontal {
		g.Stop2 = f32.Pt(float32(ir.Max.X), float32(ir.Min.Y))
	} else {
		g.Stop2 = f32.Pt(float32(ir.Min.X), float32(ir.Max.Y))
	}
	st := clip.Rect(ir).Push(c.Ops)
	g.Add(c.Ops)
	paint.PaintOp{}.Add(c.Ops)
	st.Pop()
}

// Clip restricts drawing to r until the returned func is called.
func (c *Canvas) Clip(r Rect) func() {
	st := clip.Rect(c.irect(r)).Push(c.Ops)
	return st.Pop
}

// ClipRound restricts drawing to a rounded r until the returned func is called.
func (c *Canvas) ClipRound(r Rect, rad float32) func() {
	rad = min(rad, r.W/2, r.H/2)
	st := clip.UniformRRect(c.irect(r), c.px(rad)).Push(c.Ops)
	return st.Pop
}

// Opacity draws everything until the returned func is called as one layer at alpha a.
func (c *Canvas) Opacity(a float32) func() {
	if c.SRGBBlend {
		a = float32(srgbAlpha(float64(a), 0.9))
	}
	st := paint.PushOpacity(c.Ops, a)
	return st.Pop
}

// TextStyle describes a run of text.
type TextStyle struct {
	Face    font.Typeface
	Weight  font.Weight
	Italic  bool
	Size    float32
	Color   color.NRGBA
	Spacing float32 // letter spacing in em
}

func (t TextStyle) font() font.Font {
	f := font.Font{Typeface: t.Face, Weight: t.Weight}
	if t.Italic {
		f.Style = font.Italic
	}
	return f
}

func (c *Canvas) shape(s string, t TextStyle) []text.Glyph {
	c.Shaper.LayoutString(text.Parameters{
		Font:             t.font(),
		PxPerEm:          fixed.Int26_6(t.Size * c.Scale * 64),
		MaxWidth:         1 << 24,
		DisableSpaceTrim: true,
	}, s)
	c.glyphs = c.glyphs[:0]
	for {
		g, ok := c.Shaper.NextGlyph()
		if !ok {
			break
		}
		c.glyphs = append(c.glyphs, g)
	}
	return c.glyphs
}

type metricKey struct {
	f    font.Font
	size float32
}

type metric struct{ ascent, descent float32 }

// metrics returns the ascent and descent in CSS pixels.
func (c *Canvas) fontMetrics(t TextStyle) metric {
	k := metricKey{t.font(), t.Size * c.Scale}
	if m, ok := c.metrics[k]; ok {
		return m
	}
	gs := c.shape("M", t)
	var m metric
	if len(gs) > 0 {
		m = metric{fx(gs[0].Ascent) / c.Scale, fx(gs[0].Descent) / c.Scale}
	}
	c.metrics[k] = m
	return m
}

func fx(v fixed.Int26_6) float32 { return float32(v) / 64 }

// Measure returns the advance of s in CSS pixels.
func (c *Canvas) Measure(s string, t TextStyle) float32 {
	if s == "" {
		return 0
	}
	if c.Mode != TextPaths {
		return c.measureShaped(s, t)
	}
	return c.measurePaths(s, t)
}

func (c *Canvas) measurePaths(s string, t TextStyle) float32 {
	if t.Spacing != 0 {
		var w float32
		for _, r := range s {
			w += c.measurePaths(string(r), TextStyle{t.Face, t.Weight, t.Italic, t.Size, t.Color, 0}) + t.Spacing*t.Size
		}
		return w
	}
	gs := c.shape(s, t)
	if len(gs) == 0 {
		return 0
	}
	last := gs[len(gs)-1]
	return fx(last.X+last.Advance) / c.Scale
}

// Text draws s with its baseline at y and returns the advance.
func (c *Canvas) Text(x, y float32, s string, t TextStyle) float32 {
	if s == "" {
		return 0
	}
	if c.Mode != TextPaths {
		return c.textShaped(x, y, s, t)
	}
	return c.textPaths(x, y, s, t)
}

func (c *Canvas) textPaths(x, y float32, s string, t TextStyle) float32 {
	if t.Spacing != 0 {
		x0 := x
		plain := t
		plain.Spacing = 0
		for _, r := range s {
			x += c.textPaths(x, y, string(r), plain) + t.Spacing*t.Size
		}
		return x - x0
	}
	gs := c.shape(s, t)
	if len(gs) == 0 {
		return 0
	}
	base := float32(math.Round(float64(y * c.Scale)))
	tr := op.Affine(f32.AffineId().Offset(f32.Pt(x*c.Scale+fx(gs[0].X), base))).Push(c.Ops)
	cl := clip.Outline{Path: c.Shaper.Shape(gs)}.Op().Push(c.Ops)
	paint.ColorOp{Color: c.col(t.Color)}.Add(c.Ops)
	paint.PaintOp{}.Add(c.Ops)
	cl.Pop()
	c.Shaper.Bitmaps(gs).Add(c.Ops)
	tr.Pop()
	last := gs[len(gs)-1]
	return fx(last.X+last.Advance) / c.Scale
}

// Baseline returns the baseline that centres t's content area on cy, as CSS does.
func (c *Canvas) Baseline(cy float32, t TextStyle) float32 {
	m := c.fontMetrics(t)
	return cy + (m.ascent-m.descent)/2
}

// TextMid draws s vertically centred on cy.
func (c *Canvas) TextMid(x, cy float32, s string, t TextStyle) float32 {
	return c.Text(x, c.Baseline(cy, t), s, t)
}
