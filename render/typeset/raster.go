package typeset

import (
	"image"
	"image/color"
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

// Colour is a colour glyph's image, premultiplied, placed relative to the pen as Coverage is.
type Colour struct {
	Img *image.RGBA // nil for an empty glyph
	Off image.Point
}

// IsColour reports whether face draws gid in colour: its COLR layers rather than one outline.
func IsColour(face *Face, gid GlyphID) bool { return len(face.layers(gid)) > 0 }

// RasteriseColour draws face's colour glyph gid as Rasterise draws an outline: each of its layers, bottom first, in
// its palette colour, or fg where the layer takes the text's own.
func RasteriseColour(face *Face, gid GlyphID, ppem float32, sub uint8, fg color.NRGBA) Colour {
	type layer struct {
		cov Coverage
		col color.NRGBA
	}
	var ls []layer
	var bounds image.Rectangle
	for _, l := range face.layers(gid) {
		cov := Rasterise(face, l.ID, ppem, sub)
		if cov.Mask == nil {
			continue
		}
		col := l.Color
		if l.Foreground {
			col = fg
		}
		ls = append(ls, layer{cov, col})
		bounds = bounds.Union(cov.Mask.Bounds().Add(cov.Off))
	}
	if bounds.Empty() {
		return Colour{}
	}
	img := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for _, l := range ls {
		at := l.cov.Off.Sub(bounds.Min)
		b := l.cov.Mask.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				c := l.cov.Mask.AlphaAt(x, y).A
				if c == 0 {
					continue
				}
				a := uint32(c) * uint32(l.col.A) / 255
				i := img.PixOffset(at.X+x-b.Min.X, at.Y+y-b.Min.Y)
				px := img.Pix[i : i+4 : i+4]
				inv := 255 - a
				px[0] = uint8((uint32(l.col.R)*a + uint32(px[0])*inv) / 255)
				px[1] = uint8((uint32(l.col.G)*a + uint32(px[1])*inv) / 255)
				px[2] = uint8((uint32(l.col.B)*a + uint32(px[2])*inv) / 255)
				px[3] = uint8(a + uint32(px[3])*inv/255)
			}
		}
	}
	return Colour{Img: img, Off: bounds.Min}
}
