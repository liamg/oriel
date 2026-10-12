// Package gio is the render backend on Gio: windows, input and GPU drawing.
//
// Gio stencils every path and every image drawn under a scale on every frame, so text goes
// through a glyph atlas, rounded rects, borders and glows are baked 9-slices drawn at whole
// pixels, and no image is ever drawn scaled (rendering.md).
package gio

import (
	"image"
	"image/color"
	"math"

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
	staleAt   = 120 // frames unused before a baked shape or glow can go
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
	stack  []func()
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

type imageEntry struct {
	op   paint.ImageOp
	used int
}

var _ render.Frame = (*Frame)(nil)

func NewFrame() *Frame {
	return &Frame{
		atlas:  newAtlas(),
		gamma:  textGamma{darken: 0.25},
		covs:   map[covKey]typeset.Coverage{},
		baked:  map[any]*bakedEntry{},
		images: map[*image.RGBA]*imageEntry{},
	}
}

// Begin starts a frame into ops at scale device pixels per logical pixel.
func (c *Frame) Begin(ops *op.Ops, scale float32) {
	if scale != c.scale {
		c.baked = map[any]*bakedEntry{}
		c.covs = map[covKey]typeset.Coverage{}
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
			paint.FillShape(c.ops, solid(s.Fill), clip.Rect(c.irect(r)).Op())
		}
	}
	if s.Width > 0 && s.Stroke.A > 0 {
		c.bakedShape(r, s.Radius, s.Width, s.Stroke)
	}
}

func (c *Frame) gradient(r render.Rect, g *render.Gradient) {
	ir := c.irect(r)
	lg := paint.LinearGradientOp{Color1: solid(g.From), Color2: solid(g.To), Stop1: f32.Pt(float32(ir.Min.X), float32(ir.Min.Y))}
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

func (c *Frame) Path(p *render.Path, s render.Style) {
	spec := func() clip.PathSpec {
		var cp clip.Path
		cp.Begin(c.ops)
		for _, sg := range p.Segs {
			switch sg.Op {
			case render.MoveTo:
				cp.MoveTo(c.pt(sg.Pts[0]))
			case render.LineTo:
				cp.LineTo(c.pt(sg.Pts[0]))
			case render.QuadTo:
				cp.QuadTo(c.pt(sg.Pts[0]), c.pt(sg.Pts[1]))
			case render.CubeTo:
				cp.CubeTo(c.pt(sg.Pts[0]), c.pt(sg.Pts[1]), c.pt(sg.Pts[2]))
			case render.Close:
				cp.Close()
			}
		}
		return cp.End()
	}
	if s.Fill.A > 0 {
		paint.FillShape(c.ops, solid(s.Fill), clip.Outline{Path: spec()}.Op())
	}
	if s.Width > 0 && s.Stroke.A > 0 {
		paint.FillShape(c.ops, solid(s.Stroke), clip.Stroke{Path: spec(), Width: s.Width * c.scale}.Op())
	}
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
	a = float32(srgbAlpha(float64(a), 0.9))
	c.stack = append(c.stack, paint.PushOpacity(c.ops, a).Pop)
}

func (c *Frame) Pop() {
	if n := len(c.stack); n > 0 {
		c.stack[n-1]()
		c.stack = c.stack[:n-1]
	}
}
