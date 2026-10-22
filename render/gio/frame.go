// Package gio is the render backend on Gio: windows, input and GPU drawing.
//
// Gio stencils every path and every image drawn under a scale on every frame, so text goes
// through a glyph atlas, rounded rects, borders and glows are baked 9-slices drawn at whole
// pixels, and no image is ever drawn scaled.
package gio

import (
	"image"
	"image/color"
	"math"
	"slices"

	"gioui.org/f32"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

// Cache limits, so memory stays flat however long the app runs.
const (
	maxBaked  = 512 // baked shapes and glows
	maxImages = 64  // Image ops kept for images drawn in recent frames
	maxCovs   = 8192
	maxShapes = 1024 // paths kept recorded
	staleAt   = 120  // frames unused before a baked shape or glow can go
)

// Frame draws render ops into Gio ops. It keeps its caches across frames: call Begin for each.
type Frame struct {
	ops   *op.Ops
	scale float32
	frame int

	atlas  *atlas
	gamma  textGamma
	covs   map[covKey]typeset.Coverage
	baked  map[any]*bakedEntry
	images map[*image.RGBA]*imageEntry
	shapes map[shapeKey]*shapeEntry
	geo    []float32 // a path's geometry, reused from path to path
	stack  []func()

	backdrop color.NRGBA
	alphas   map[[2]color.NRGBA]uint8
}

type covKey struct {
	face int32
	gid  typeset.GlyphID
	ppem int32 // 26.6 device pixels per em
	sub  uint8
}

type glyphKey struct {
	covKey
	col color.NRGBA
}

type bakedEntry struct {
	n    *nine
	used int
}

// shapeKey is a path's fill or stroke: its geometry's hash, from its device-pixel origin, and its paint.
type shapeKey struct {
	geo   uint64
	col   color.NRGBA
	width float32 // the stroke's, in device pixels; 0 fills
}

// shapeEntry is a path's fill or stroke recorded once in ops of its own, which Gio caches what it stencils from while
// they're drawn frame after frame.
type shapeEntry struct {
	ops  op.Ops
	call op.CallOp
	geo  []float32
	used int
}

type imageEntry struct {
	op   paint.ImageOp
	used int
}

var _ render.Frame = (*Frame)(nil)

func NewFrame() *Frame {
	return &Frame{
		atlas:    newAtlas(),
		gamma:    textGamma{darken: 0.25},
		covs:     map[covKey]typeset.Coverage{},
		baked:    map[any]*bakedEntry{},
		images:   map[*image.RGBA]*imageEntry{},
		shapes:   map[shapeKey]*shapeEntry{},
		alphas:   map[[2]color.NRGBA]uint8{},
		backdrop: color.NRGBA{A: 255},
	}
}

// Backdrop sets what translucent fills and opacity layers are corrected over; black until set.
func (c *Frame) Backdrop(col color.NRGBA) { c.backdrop = col }

// Begin starts a frame into ops at scale device pixels per logical pixel.
func (c *Frame) Begin(ops *op.Ops, scale float32) {
	if scale != c.scale {
		c.baked = map[any]*bakedEntry{}
		c.covs = map[covKey]typeset.Coverage{}
		c.shapes = map[shapeKey]*shapeEntry{}
	}
	c.ops, c.scale = ops, scale
	c.frame++
	c.stack = c.stack[:0]
	c.atlas.beginFrame()
	for k, im := range c.images {
		if c.frame-im.used > 2 {
			delete(c.images, k)
		}
	}
	if len(c.baked) > maxBaked {
		for k, b := range c.baked {
			if c.frame-b.used > staleAt || len(c.baked) > 2*maxBaked {
				delete(c.baked, k)
			}
		}
	}
	if len(c.shapes) > maxShapes {
		for k, e := range c.shapes {
			if c.frame-e.used > staleAt || len(c.shapes) > 2*maxShapes {
				delete(c.shapes, k)
			}
		}
	}
	if len(c.covs) > maxCovs {
		c.covs = map[covKey]typeset.Coverage{}
	}
}

// End pops anything left pushed.
func (c *Frame) End() {
	for len(c.stack) > 0 {
		c.Pop()
	}
}

func (c *Frame) Scale() float32 { return c.scale }

func (c *Frame) px(v float32) int { return int(math.Round(float64(v * c.scale))) }

func (c *Frame) irect(r render.Rect) image.Rectangle {
	return image.Rect(c.px(r.Min.X), c.px(r.Min.Y), c.px(r.Max.X), c.px(r.Max.Y))
}

func (c *Frame) pt(p render.Point) f32.Point { return f32.Pt(p.X*c.scale, p.Y*c.scale) }

func (c *Frame) Rect(r render.Rect, s render.Style) {
	if g := s.Gradient; g != nil {
		c.gradient(r, g)
	} else if s.Fill.A > 0 {
		if s.Radius > 0 {
			c.bakedShape(r, s.Radius, 0, s.Fill)
		} else {
			paint.FillShape(c.ops, c.solid(s.Fill), clip.Rect(c.irect(r)).Op())
		}
	}
	if s.Width > 0 && s.Stroke.A > 0 {
		c.bakedShape(r, s.Radius, s.Width, s.Stroke)
	}
}

func (c *Frame) gradient(r render.Rect, g *render.Gradient) {
	ir := c.irect(r)
	lg := paint.LinearGradientOp{Color1: c.solid(g.From), Color2: c.solid(g.To), Stop1: f32.Pt(float32(ir.Min.X), float32(ir.Min.Y))}
	if g.Horizontal {
		lg.Stop2 = f32.Pt(float32(ir.Max.X), float32(ir.Min.Y))
	} else {
		lg.Stop2 = f32.Pt(float32(ir.Min.X), float32(ir.Max.Y))
	}
	st := clip.Rect(ir).Push(c.ops)
	lg.Add(c.ops)
	paint.PaintOp{}.Add(c.ops)
	st.Pop()
}

// Path draws p from a recording of it at a whole device pixel, kept while it's drawn: a path drawn the same frame
// after frame, wherever it moves, is stencilled once rather than every frame.
func (c *Frame) Path(p *render.Path, s render.Style) {
	if len(p.Segs) == 0 {
		return
	}
	org, h := c.geometry(p)
	if s.Fill.A > 0 {
		c.shape(org, shapeKey{geo: h, col: c.solid(s.Fill)})
	}
	if s.Width > 0 && s.Stroke.A > 0 {
		c.shape(org, shapeKey{geo: h, col: c.solid(s.Stroke), width: s.Width * c.scale})
	}
}

// segPts is how many points each segment op takes.
var segPts = [...]int{render.MoveTo: 1, render.LineTo: 1, render.QuadTo: 2, render.CubeTo: 3, render.Close: 0}

// geometry puts p into c.geo in device pixels from its origin, the whole pixel at or above and left of it, and
// answers the origin and the geometry's hash.
func (c *Frame) geometry(p *render.Path) (image.Point, uint64) {
	minX, minY := float32(math.Inf(1)), float32(math.Inf(1))
	for _, sg := range p.Segs {
		for _, pt := range sg.Pts[:segPts[sg.Op]] {
			minX, minY = min(minX, pt.X), min(minY, pt.Y)
		}
	}
	var org image.Point
	if !math.IsInf(float64(minX), 1) {
		org = image.Pt(int(math.Floor(float64(minX*c.scale))), int(math.Floor(float64(minY*c.scale))))
	}
	ox, oy := float32(org.X), float32(org.Y)
	c.geo = c.geo[:0]
	h := uint64(14695981039346656037)
	put := func(v float32) {
		c.geo = append(c.geo, v)
		h = (h ^ uint64(math.Float32bits(v))) * 1099511628211
	}
	for _, sg := range p.Segs {
		put(float32(sg.Op))
		for _, pt := range sg.Pts[:segPts[sg.Op]] {
			put(pt.X*c.scale - ox)
			put(pt.Y*c.scale - oy)
		}
	}
	return org, h
}

// shape draws the path in c.geo at org, recording it the first time.
func (c *Frame) shape(org image.Point, k shapeKey) {
	e := c.shapes[k]
	if e == nil || !slices.Equal(e.geo, c.geo) {
		e = &shapeEntry{geo: slices.Clone(c.geo)}
		m := op.Record(&e.ops)
		var cp clip.Path
		cp.Begin(&e.ops)
		pt := func(i int) f32.Point { return f32.Pt(e.geo[i], e.geo[i+1]) }
		for i := 0; i < len(e.geo); {
			sop := render.SegOp(e.geo[i])
			i++
			switch sop {
			case render.MoveTo:
				cp.MoveTo(pt(i))
			case render.LineTo:
				cp.LineTo(pt(i))
			case render.QuadTo:
				cp.QuadTo(pt(i), pt(i+2))
			case render.CubeTo:
				cp.CubeTo(pt(i), pt(i+2), pt(i+4))
			case render.Close:
				cp.Close()
			}
			i += 2 * segPts[sop]
		}
		spec := cp.End()
		if k.width > 0 {
			paint.FillShape(&e.ops, k.col, clip.Stroke{Path: spec, Width: k.width}.Op())
		} else {
			paint.FillShape(&e.ops, k.col, clip.Outline{Path: spec}.Op())
		}
		e.call = m.Stop()
		c.shapes[k] = e
	}
	e.used = c.frame
	st := op.Offset(org).Push(c.ops)
	e.call.Add(c.ops)
	st.Pop()
}

// Glyphs draws each glyph from the atlas at a whole device pixel and a quarter-pixel subpixel step,
// with the baseline snapped to a device pixel. Images blend like CSS already, so the colour goes in
// without solid's remap.
func (c *Frame) Glyphs(run *typeset.Run, at render.Point, col color.NRGBA) {
	ppem := run.Font.Size * c.scale
	base := c.px(at.Y)
	pen := at.X * c.scale
	for _, g := range run.Glyphs {
		if g.Face == nil {
			continue
		}
		ix, sub := typeset.SubStep(pen + g.X*c.scale)
		ck := covKey{face: g.Face.ID, gid: g.ID, ppem: int32(math.Round(float64(ppem * 64))), sub: sub}
		gk := glyphKey{ck, col}
		e, ok := c.atlas.Lookup(gk)
		if !ok {
			cov, ok := c.covs[ck]
			if !ok {
				cov = typeset.Rasterise(g.Face, g.ID, ppem, sub)
				c.covs[ck] = cov
			}
			if cov.Mask == nil {
				e = c.atlas.Add(gk, image.Point{}, image.Point{}, nil)
			} else {
				e = c.atlas.Add(gk, cov.Mask.Bounds().Size(), cov.Off, func(dst *image.RGBA, p image.Point) {
					tint(dst, p, cov.Mask, col, &c.gamma)
				})
			}
		}
		y := base + int(math.Round(float64(g.Y*c.scale)))
		c.atlas.draw(c.ops, e, image.Pt(ix+e.Off.X, y+e.Off.Y), image.Rectangle{})
	}
}

func (c *Frame) Image(img *image.RGBA, at render.Point) {
	if img == nil || img.Rect.Empty() {
		return
	}
	im, ok := c.images[img]
	if !ok {
		if len(c.images) >= maxImages {
			for k, v := range c.images {
				if v.used < c.frame {
					delete(c.images, k)
				}
			}
		}
		im = &imageEntry{op: paint.NewImageOp(img)}
		im.op.Filter = paint.FilterNearest
		c.images[img] = im
	}
	im.used = c.frame
	defer op.Offset(image.Pt(c.px(at.X), c.px(at.Y))).Push(c.ops).Pop()
	im.op.Add(c.ops)
	paint.PaintOp{}.Add(c.ops)
}

func (c *Frame) Effect(kind render.EffectKind, r render.Rect, blur float32, col color.NRGBA) {
	switch kind {
	case render.Glow:
		c.glow(r, blur, col)
	case render.GlowDisc:
		c.glowDisc((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, r.Dx()/2, blur, col)
	}
}

func (c *Frame) PushClip(r render.Rect, radius float32) {
	ir := c.irect(r)
	if radius > 0 {
		rad := min(radius, r.Dx()/2, r.Dy()/2)
		c.stack = append(c.stack, clip.UniformRRect(ir, c.px(rad)).Push(c.ops).Pop)
		return
	}
	c.stack = append(c.stack, clip.Rect(ir).Push(c.ops).Pop)
}

func (c *Frame) PushOpacity(a float32) {
	c.stack = append(c.stack, paint.PushOpacity(c.ops, c.layerAlpha(a)).Pop)
}

func (c *Frame) Pop() {
	if n := len(c.stack); n > 0 {
		c.stack[n-1]()
		c.stack = c.stack[:n-1]
	}
}
