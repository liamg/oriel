// Package scenes draws the reference screens (01-ide and the budget scenes from shell.md) through
// render.Frame, so any backend can be checked against the mockups and measured.
package scenes

import (
	"bytes"
	"encoding/binary"
	"hash/fnv"
	"image"
	"image/color"
	"math"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

const (
	Mono    = typeset.Mono
	UI      = typeset.UI
	Display = typeset.Display
)

// Canvas draws in CSS pixels on a render.Frame.
type Canvas struct {
	F  render.Frame
	Sh typeset.Shaper

	bars  map[uint64]*barsImage
	frame int
}

func NewCanvas(sh typeset.Shaper) *Canvas { return &Canvas{Sh: sh, bars: map[uint64]*barsImage{}} }

// Begin starts drawing a frame into f, keeping caches from earlier frames.
func (c *Canvas) Begin(f render.Frame) {
	c.F = f
	c.frame++
	for k, b := range c.bars {
		if c.frame-b.used > 2 {
			delete(c.bars, k)
		}
	}
}

// Rect is a rectangle in CSS pixels.
type Rect struct{ X, Y, W, H float32 }

func (r Rect) r() render.Rect { return render.XYWH(r.X, r.Y, r.W, r.H) }

func (c *Canvas) Fill(r Rect, col color.NRGBA) { c.F.Rect(r.r(), render.Style{Fill: col}) }

// FillFree paints r at its exact position, antialiased rather than snapped to device pixels.
func (c *Canvas) FillFree(r Rect, col color.NRGBA) {
	p := new(render.Path).MoveTo(render.Point{X: r.X, Y: r.Y}).LineTo(render.Point{X: r.X + r.W, Y: r.Y}).
		LineTo(render.Point{X: r.X + r.W, Y: r.Y + r.H}).LineTo(render.Point{X: r.X, Y: r.Y + r.H}).Close()
	c.F.Path(p, render.Style{Fill: col})
}

func (c *Canvas) RRect(r Rect, rad float32, col color.NRGBA) {
	c.F.Rect(r.r(), render.Style{Fill: col, Radius: rad})
}

// Border strokes the inside of r, like a CSS border.
func (c *Canvas) Border(r Rect, rad, width float32, col color.NRGBA) {
	c.F.Rect(r.r(), render.Style{Radius: rad, Stroke: col, Width: width})
}

// Line strokes a straight line, unsnapped, so a 0.5 px hairline stays 0.5 px.
func (c *Canvas) Line(x0, y0, x1, y1, width float32, col color.NRGBA) {
	p := new(render.Path).MoveTo(render.Point{X: x0, Y: y0}).LineTo(render.Point{X: x1, Y: y1})
	c.F.Path(p, render.Style{Stroke: col, Width: width})
}

// Wavy draws a squiggle underline from x0 to x1 at y.
func (c *Canvas) Wavy(x0, x1, y float32, col color.NRGBA) {
	p := new(render.Path).MoveTo(render.Point{X: x0, Y: y})
	const half = 2.0
	for x, up := x0, true; x < x1; x, up = x+half, !up {
		dy := float32(1.5)
		if up {
			dy = -dy
		}
		p.QuadTo(render.Point{X: x + half/2, Y: y + dy}, render.Point{X: x + half, Y: y})
	}
	c.F.Path(p, render.Style{Stroke: col, Width: 1})
}

func (c *Canvas) Ring(cx, cy, r, width float32, col color.NRGBA) {
	c.F.Path(render.Ellipse(render.XYWH(cx-r, cy-r, 2*r, 2*r)), render.Style{Stroke: col, Width: width})
}

func (c *Canvas) Disc(cx, cy, r float32, col color.NRGBA) {
	c.F.Path(render.Ellipse(render.XYWH(cx-r, cy-r, 2*r, 2*r)), render.Style{Fill: col})
}

// Gradient fills r with a two-stop linear gradient, horizontal or vertical.
func (c *Canvas) Gradient(r Rect, horizontal bool, from, to color.NRGBA) {
	c.F.Rect(r.r(), render.Style{Gradient: &render.Gradient{From: from, To: to, Horizontal: horizontal}})
}

// Clip restricts drawing to r until the returned func is called.
func (c *Canvas) Clip(r Rect) func() {
	c.F.PushClip(r.r(), 0)
	return c.F.Pop
}

func (c *Canvas) ClipRound(r Rect, rad float32) func() {
	c.F.PushClip(r.r(), rad)
	return c.F.Pop
}

// Opacity draws everything until the returned func is called as one layer at alpha a.
func (c *Canvas) Opacity(a float32) func() {
	c.F.PushOpacity(a)
	return c.F.Pop
}

// Glow draws a `box-shadow: 0 0 blur col` around r.
func (c *Canvas) Glow(r Rect, blur float32, col color.NRGBA) {
	c.F.Effect(render.Glow, r.r(), blur, col)
}

// GlowDisc draws a glow around a circle of radius r centred on (cx, cy).
func (c *Canvas) GlowDisc(cx, cy, r, blur float32, col color.NRGBA) {
	c.F.Effect(render.GlowDisc, render.XYWH(cx-r, cy-r, 2*r, 2*r), blur, col)
}

// TextStyle describes a run of text.
type TextStyle struct {
	Face    string
	Weight  typeset.Weight
	Italic  bool
	Size    float32
	Color   color.NRGBA
	Spacing float32 // letter spacing in em
}

func (t TextStyle) spec() typeset.FontSpec {
	return typeset.FontSpec{Family: t.Face, Weight: t.Weight, Italic: t.Italic, Size: t.Size, Tracking: t.Spacing}
}

// Measure returns the advance of s.
func (c *Canvas) Measure(s string, t TextStyle) float32 {
	if s == "" {
		return 0
	}
	return c.Sh.Shape(s, t.spec()).Advance
}

// Text draws s with its baseline at y and returns the advance.
func (c *Canvas) Text(x, y float32, s string, t TextStyle) float32 {
	if s == "" {
		return 0
	}
	run := c.Sh.Shape(s, t.spec())
	c.F.Glyphs(run, render.Point{X: x, Y: y}, t.Color)
	return run.Advance
}

// Baseline returns the baseline that centres t's content area on cy, as CSS does.
func (c *Canvas) Baseline(cy float32, t TextStyle) float32 {
	r := c.Sh.Shape("M", t.spec())
	return cy + (r.Ascent-r.Descent)/2
}

// TextMid draws s vertically centred on cy.
func (c *Canvas) TextMid(x, cy float32, s string, t TextStyle) float32 {
	return c.Text(x, c.Baseline(cy, t), s, t)
}

type barsImage struct {
	img  *image.RGBA
	at   render.Point
	used int
}

// Bars draws a bar chart: bar i is w wide at x+i·step, vals[i]·maxH tall, standing on bottom. The chart is
// one image in device pixels, redrawn only when vals change.
func (c *Canvas) Bars(x, bottom, step, w, maxH float32, vals []float32, col color.NRGBA) {
	s := c.F.Scale()
	h := fnv.New64a()
	var b [4]byte
	for _, f := range []float32{x, bottom, step, w, maxH, s} {
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
		px := func(v float32) int { return int(math.Round(float64(v * s))) }
		dev := func(r Rect) image.Rectangle { return image.Rect(px(r.X), px(r.Y), px(r.X+r.W), px(r.Y+r.H)) }
		area := dev(Rect{x, bottom - maxH, float32(len(vals)) * step, maxH})
		img := image.NewRGBA(image.Rectangle{Max: area.Size()})
		a := uint32(col.A)
		pix := []byte{uint8(uint32(col.R) * a / 255), uint8(uint32(col.G) * a / 255), uint8(uint32(col.B) * a / 255), col.A}
		row := bytes.Repeat(pix, area.Dx())
		for i, v := range vals {
			r := dev(Rect{x + float32(i)*step, bottom - v*maxH, w, v * maxH}).Sub(area.Min).Intersect(img.Rect)
			for y := r.Min.Y; y < r.Max.Y; y++ {
				off := img.PixOffset(r.Min.X, y)
				copy(img.Pix[off:off+4*r.Dx()], row[:4*r.Dx()])
			}
		}
		bi = &barsImage{img: img, at: render.Point{X: float32(area.Min.X) / s, Y: float32(area.Min.Y) / s}}
		c.bars[k] = bi
	}
	bi.used = c.frame
	c.F.Image(bi.img, bi.at)
}
