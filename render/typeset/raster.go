package typeset

import (
	"image"
	"math"

	ot "github.com/go-text/typesetting/font/opentype"
	"golang.org/x/image/vector"
)

// SubpixelSteps is how many horizontal subpixel positions a glyph is rasterised at.
const SubpixelSteps = 4

// Coverage is a glyph's antialiased mask, placed relative to the pen: the pen sits at
// (-Off.X, -Off.Y) in Mask, with the baseline at y = -Off.Y.
type Coverage struct {
	Mask *image.Alpha // nil for empty glyphs such as space
	Off  image.Point
}

// Rasterise draws face's glyph gid at ppem device pixels per em, shifted right by sub/SubpixelSteps px.
func Rasterise(face *Face, gid GlyphID, ppem float32, sub uint8) Coverage {
	segs, ok := face.outline(gid)
	if !ok {
		return Coverage{}
	}
	return rasterise(segs, ppem/float32(face.Upem()), float32(sub)/SubpixelSteps)
}

// SubStep splits a device x into a whole pixel and a subpixel step.
func SubStep(x float32) (int, uint8) {
	ix := float32(math.Floor(float64(x)))
	s := int((x-ix)*SubpixelSteps + 0.5)
	if s == SubpixelSteps {
		return int(ix) + 1, 0
	}
	return int(ix), uint8(s)
}

// rasterise draws an outline (font units, y up) at scale px per unit, shifted right by sub px.
func rasterise(segs []ot.Segment, scale, sub float32) Coverage {
	if len(segs) == 0 {
		return Coverage{}
	}
	minX, minY := float32(math.Inf(1)), float32(math.Inf(1))
	maxX, maxY := float32(math.Inf(-1)), float32(math.Inf(-1))
	for _, s := range segs {
		for _, p := range s.ArgsSlice() {
			x, y := p.X*scale+sub, -p.Y*scale
			minX, maxX = min(minX, x), max(maxX, x)
			minY, maxY = min(minY, y), max(maxY, y)
		}
	}
	x0, y0 := int(math.Floor(float64(minX))), int(math.Floor(float64(minY)))
	x1, y1 := int(math.Ceil(float64(maxX))), int(math.Ceil(float64(maxY)))
	w, h := x1-x0, y1-y0
	if w <= 0 || h <= 0 {
		return Coverage{}
	}
	var r vector.Rasterizer
	r.Reset(w, h)
	pt := func(p ot.SegmentPoint) (float32, float32) {
		return p.X*scale + sub - float32(x0), -p.Y*scale - float32(y0)
	}
	for _, s := range segs {
		a := s.Args
		switch s.Op {
		case ot.SegmentOpMoveTo:
			r.MoveTo(pt(a[0]))
		case ot.SegmentOpLineTo:
			r.LineTo(pt(a[0]))
		case ot.SegmentOpQuadTo:
			bx, by := pt(a[0])
			cx, cy := pt(a[1])
			r.QuadTo(bx, by, cx, cy)
		case ot.SegmentOpCubeTo:
			bx, by := pt(a[0])
			cx, cy := pt(a[1])
			dx, dy := pt(a[2])
			r.CubeTo(bx, by, cx, cy, dx, dy)
		}
	}
	r.ClosePath()
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	r.Draw(mask, mask.Bounds(), image.Opaque, image.Point{})
	return Coverage{Mask: mask, Off: image.Pt(x0, y0)}
}
