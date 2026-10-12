// Package raster draws display lists on the CPU, blending in sRGB as CSS (and so the mockups) does.
// It's the headless backend's pixels: UI tests and theme screenshots render without a GPU.
package raster

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/vector"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/bake"
	"github.com/liamg/oriel/render/typeset"
)

// Frame is a render.Frame onto an RGBA image (premultiplied sRGB), in device pixels at Scale.
type Frame struct {
	dst   *image.RGBA
	scale float32
	stack []entry
	covs  map[covKey]typeset.Coverage
}

type entry struct {
	clip   bool
	rect   image.Rectangle // clip
	radius float32         // clip, device pixels
	under  *image.RGBA     // opacity: the image the layer composites onto
	alpha  float32
}

type covKey struct {
	face int32
	gid  typeset.GlyphID
	ppem int32
	sub  uint8
}

var _ render.Frame = (*Frame)(nil)

// New returns a frame of size device pixels at scale.
func New(size image.Point, scale float32) *Frame {
	return &Frame{dst: image.NewRGBA(image.Rectangle{Max: size}), scale: scale, covs: map[covKey]typeset.Coverage{}}
}

// Render draws l at scale into a new image of the logical size, over bg.
func Render(l *render.List, size render.Point, scale float32, bg color.NRGBA) *image.RGBA {
	f := New(image.Pt(int(size.X*scale+0.5), int(size.Y*scale+0.5)), scale)
	f.Rect(render.Rect{Max: size}, render.Style{Fill: bg})
	l.Replay(f)
	return f.Result()
}

// Result is what's drawn, with anything still pushed popped.
func (f *Frame) Result() *image.RGBA {
	for len(f.stack) > 0 {
		f.Pop()
	}
	return f.dst
}

func (f *Frame) Scale() float32                       { return f.scale }
func (f *Frame) Backdrop(color.NRGBA)                 {}
func (f *Frame) px(v float32) int                     { return int(math.Round(float64(v * f.scale))) }
func (f *Frame) pt(p render.Point) (float32, float32) { return p.X * f.scale, p.Y * f.scale }

func (f *Frame) irect(r render.Rect) image.Rectangle {
	return image.Rect(f.px(r.Min.X), f.px(r.Min.Y), f.px(r.Max.X), f.px(r.Max.Y))
}

// clipAt is how much of pixel (x, y) the clip stack lets through.
func (f *Frame) clipAt(x, y int) float32 {
	c := float32(1)
	for _, e := range f.stack {
		if !e.clip {
			continue
		}
		if !image.Pt(x, y).In(e.rect) {
			return 0
		}
		if e.radius > 0 {
			c *= rrectCoverage(x, y, e.rect, e.radius, 0)
		}
	}
	return c
}

// clipBounds is the area any drawing can reach.
func (f *Frame) clipBounds() image.Rectangle {
	b := f.dst.Rect
	for _, e := range f.stack {
		if e.clip {
			b = b.Intersect(e.rect)
		}
	}
	return b
}

// blend puts col at coverage cov over pixel (x, y), source-over in sRGB.
func (f *Frame) blend(x, y int, col color.NRGBA, cov float32) {
	cov *= f.clipAt(x, y)
	if cov <= 0 {
		return
	}
	a := float32(col.A) / 255 * min(cov, 1)
	i := f.dst.PixOffset(x, y)
	p := f.dst.Pix[i : i+4 : i+4]
	inv := 1 - a
	p[0] = uint8(float32(col.R)*a + float32(p[0])*inv + 0.5)
	p[1] = uint8(float32(col.G)*a + float32(p[1])*inv + 0.5)
	p[2] = uint8(float32(col.B)*a + float32(p[2])*inv + 0.5)
	p[3] = uint8(255*a + float32(p[3])*inv + 0.5)
}

// blendPremul puts a premultiplied pixel over (x, y).
func (f *Frame) blendPremul(x, y int, r, g, b, a uint8) {
	cov := f.clipAt(x, y)
	if cov <= 0 || a == 0 && r == 0 && g == 0 && b == 0 {
		return
	}
	i := f.dst.PixOffset(x, y)
	p := f.dst.Pix[i : i+4 : i+4]
	inv := 1 - float32(a)/255*cov
	p[0] = uint8(float32(r)*cov + float32(p[0])*inv + 0.5)
	p[1] = uint8(float32(g)*cov + float32(p[1])*inv + 0.5)
	p[2] = uint8(float32(b)*cov + float32(p[2])*inv + 0.5)
	p[3] = uint8(float32(a)*cov + float32(p[3])*inv + 0.5)
}

// rrectCoverage is how much of pixel (x, y) lies in r with corner radius rad, less what lies in r inset by w
// (a border, when w > 0). Pixels away from the corners are exact; corners are supersampled 4×4.
func rrectCoverage(x, y int, r image.Rectangle, rad, w float32) float32 {
	in := func(px, py float32, x0, y0, x1, y1, rad float32) bool {
		if px < x0 || py < y0 || px > x1 || py > y1 {
			return false
		}
		cx, cy := min(max(px, x0+rad), x1-rad), min(max(py, y0+rad), y1-rad)
		dx, dy := px-cx, py-cy
		return dx*dx+dy*dy <= rad*rad
	}
	x0, y0, x1, y1 := float32(r.Min.X), float32(r.Min.Y), float32(r.Max.X), float32(r.Max.Y)
	rad = min(rad, (x1-x0)/2, (y1-y0)/2)
	fx, fy := float32(x), float32(y)
	nearCorner := func(rad float32) bool {
		return (fx < x0+rad+1 || fx+1 > x1-rad-1) && (fy < y0+rad+1 || fy+1 > y1-rad-1)
	}
	if !nearCorner(rad) && (w <= 0 || !nearCorner(max(rad-w, 0))) {
		// Straight edges: exact area by axis-aligned overlap.
		area := overlap(fx, fy, x0, y0, x1, y1)
		if w > 0 {
			area -= overlap(fx, fy, x0+w, y0+w, x1-w, y1-w)
		}
		return max(area, 0)
	}
	const ss = 4
	var n int
	for sy := range ss {
		for sx := range ss {
			px := fx + (float32(sx)+0.5)/ss
			py := fy + (float32(sy)+0.5)/ss
			if !in(px, py, x0, y0, x1, y1, rad) {
				continue
			}
			if w > 0 && in(px, py, x0+w, y0+w, x1-w, y1-w, max(rad-w, 0)) {
				continue
			}
			n++
		}
	}
	return float32(n) / (ss * ss)
}

// overlap is how much of the unit pixel at (x, y) lies in the box.
func overlap(x, y, x0, y0, x1, y1 float32) float32 {
	if x1 <= x0 || y1 <= y0 {
		return 0
	}
	w := min(x+1, x1) - max(x, x0)
	h := min(y+1, y1) - max(y, y0)
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}

func (f *Frame) Rect(r render.Rect, s render.Style) {
	ir := f.irect(r)
	area := ir.Intersect(f.clipBounds())
	rad := float32(f.px(min(s.Radius, r.Dx()/2, r.Dy()/2)))
	switch {
	case s.Gradient != nil:
		g := s.Gradient
		for y := area.Min.Y; y < area.Max.Y; y++ {
			for x := area.Min.X; x < area.Max.X; x++ {
				t := float32(y-ir.Min.Y) + 0.5
				span := float32(ir.Dy())
				if g.Horizontal {
					t, span = float32(x-ir.Min.X)+0.5, float32(ir.Dx())
				}
				f.blend(x, y, lerp(g.From, g.To, t/span), 1)
			}
		}
	case s.Fill.A > 0:
		for y := area.Min.Y; y < area.Max.Y; y++ {
			for x := area.Min.X; x < area.Max.X; x++ {
				cov := float32(1)
				if rad > 0 {
					cov = rrectCoverage(x, y, ir, rad, 0)
				}
				f.blend(x, y, s.Fill, cov)
			}
		}
	}
	if s.Width > 0 && s.Stroke.A > 0 {
		w := float32(f.px(s.Width))
		if w < 1 {
			w = s.Width * f.scale
		}
		for y := area.Min.Y; y < area.Max.Y; y++ {
			for x := area.Min.X; x < area.Max.X; x++ {
				f.blend(x, y, s.Stroke, rrectCoverage(x, y, ir, rad, w))
			}
		}
	}
}

// lerp interpolates premultiplied, as CSS gradients do.
func lerp(a, b color.NRGBA, t float32) color.NRGBA {
	t = max(0, min(1, t))
	aa, ba := float32(a.A)/255, float32(b.A)/255
	al := aa + (ba-aa)*t
	if al <= 0 {
		return color.NRGBA{}
	}
	ch := func(x, y uint8) uint8 {
		return uint8((float32(x)*aa+(float32(y)*ba-float32(x)*aa)*t)/al + 0.5)
	}
	return color.NRGBA{R: ch(a.R, b.R), G: ch(a.G, b.G), B: ch(a.B, b.B), A: uint8(al*255 + 0.5)}
}

func (f *Frame) Path(p *render.Path, s render.Style) {
	polys := f.flatten(p)
	if s.Fill.A > 0 {
		f.fillPolys(polys, s.Fill)
	}
	if s.Width > 0 && s.Stroke.A > 0 {
		f.fillPolys(stroke(polys, s.Width*f.scale), s.Stroke)
	}
}

type pt struct{ x, y float32 }

type poly struct {
	pts    []pt
	closed bool
}

// flatten turns a path into polylines in device pixels.
func (f *Frame) flatten(p *render.Path) []poly {
	var out []poly
	var cur poly
	var at pt
	flush := func() {
		if len(cur.pts) > 1 {
			out = append(out, cur)
		}
		cur = poly{}
	}
	dev := func(q render.Point) pt { x, y := f.pt(q); return pt{x, y} }
	for _, s := range p.Segs {
		switch s.Op {
		case render.MoveTo:
			flush()
			at = dev(s.Pts[0])
			cur.pts = []pt{at}
		case render.LineTo:
			at = dev(s.Pts[0])
			cur.pts = append(cur.pts, at)
		case render.QuadTo:
			c, to := dev(s.Pts[0]), dev(s.Pts[1])
			n := steps(at, c, to)
			for i := 1; i <= n; i++ {
				t := float32(i) / float32(n)
				u := 1 - t
				cur.pts = append(cur.pts, pt{u*u*at.x + 2*u*t*c.x + t*t*to.x, u*u*at.y + 2*u*t*c.y + t*t*to.y})
			}
			at = to
		case render.CubeTo:
			c1, c2, to := dev(s.Pts[0]), dev(s.Pts[1]), dev(s.Pts[2])
			n := steps(at, c1, c2, to)
			for i := 1; i <= n; i++ {
				t := float32(i) / float32(n)
				u := 1 - t
				cur.pts = append(cur.pts, pt{
					u*u*u*at.x + 3*u*u*t*c1.x + 3*u*t*t*c2.x + t*t*t*to.x,
					u*u*u*at.y + 3*u*u*t*c1.y + 3*u*t*t*c2.y + t*t*t*to.y,
				})
			}
			at = to
		case render.Close:
			cur.closed = true
			flush()
		}
	}
	flush()
	return out
}

// steps is how many lines to flatten a curve into: about one per two pixels of its control polygon.
func steps(ps ...pt) int {
	var l float32
	for i := 1; i < len(ps); i++ {
		l += float32(math.Hypot(float64(ps[i].x-ps[i-1].x), float64(ps[i].y-ps[i-1].y)))
	}
	return max(2, min(64, int(l/2)))
}

// stroke outlines polylines w wide: a quad per segment and a disc at each joint, filled as one.
func stroke(polys []poly, w float32) []poly {
	h := w / 2
	var out []poly
	disc := func(c pt) {
		const n = 12
		var d poly
		for i := range n {
			a := float64(i) / n * 2 * math.Pi
			d.pts = append(d.pts, pt{c.x + h*float32(math.Cos(a)), c.y + h*float32(math.Sin(a))})
		}
		d.closed = true
		out = append(out, d)
	}
	for _, p := range polys {
		pts := p.pts
		if p.closed {
			pts = append(pts, pts[0])
		}
		for i := 1; i < len(pts); i++ {
			a, b := pts[i-1], pts[i]
			dx, dy := b.x-a.x, b.y-a.y
			l := float32(math.Hypot(float64(dx), float64(dy)))
			if l == 0 {
				continue
			}
			nx, ny := -dy/l*h, dx/l*h
			out = append(out, poly{pts: []pt{{a.x + nx, a.y + ny}, {b.x + nx, b.y + ny}, {b.x - nx, b.y - ny}, {a.x - nx, a.y - ny}}, closed: true})
			if i > 1 || p.closed {
				disc(a)
			}
		}
	}
	return out
}

// fillPolys fills each polygon separately and takes the most coverage at each pixel, so overlaps don't
// double up.
func (f *Frame) fillPolys(polys []poly, col color.NRGBA) {
	if len(polys) == 0 {
		return
	}
	minX, minY := float32(math.Inf(1)), float32(math.Inf(1))
	maxX, maxY := float32(math.Inf(-1)), float32(math.Inf(-1))
	for _, p := range polys {
		for _, q := range p.pts {
			minX, maxX = min(minX, q.x), max(maxX, q.x)
			minY, maxY = min(minY, q.y), max(maxY, q.y)
		}
	}
	box := image.Rect(int(math.Floor(float64(minX))), int(math.Floor(float64(minY))), int(math.Ceil(float64(maxX))), int(math.Ceil(float64(maxY)))).Intersect(f.clipBounds())
	if box.Empty() {
		return
	}
	acc := image.NewAlpha(box.Sub(box.Min))
	mask := image.NewAlpha(acc.Rect)
	var r vector.Rasterizer
	for _, p := range polys {
		r.Reset(box.Dx(), box.Dy())
		for i, q := range p.pts {
			x, y := q.x-float32(box.Min.X), q.y-float32(box.Min.Y)
			if i == 0 {
				r.MoveTo(x, y)
			} else {
				r.LineTo(x, y)
			}
		}
		r.ClosePath()
		clear(mask.Pix)
		r.Draw(mask, mask.Rect, image.Opaque, image.Point{})
		for i, v := range mask.Pix {
			acc.Pix[i] = max(acc.Pix[i], v)
		}
	}
	for y := 0; y < box.Dy(); y++ {
		for x := 0; x < box.Dx(); x++ {
			if c := acc.Pix[y*acc.Stride+x]; c > 0 {
				f.blend(box.Min.X+x, box.Min.Y+y, col, float32(c)/255)
			}
		}
	}
}

func (f *Frame) Glyphs(run *typeset.Run, at render.Point, col color.NRGBA) {
	ppem := run.Font.Size * f.scale
	base := f.px(at.Y)
	pen := at.X * f.scale
	for _, g := range run.Glyphs {
		if g.Face == nil {
			continue
		}
		ix, sub := typeset.SubStep(pen + g.X*f.scale)
		k := covKey{g.Face.ID, g.ID, int32(math.Round(float64(ppem * 64))), sub}
		cov, ok := f.covs[k]
		if !ok {
			cov = typeset.Rasterise(g.Face, g.ID, ppem, sub)
			f.covs[k] = cov
		}
		if cov.Mask == nil {
			continue
		}
		ox := ix + cov.Off.X
		oy := base + int(math.Round(float64(g.Y*f.scale))) + cov.Off.Y
		b := cov.Mask.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				if c := cov.Mask.AlphaAt(x, y).A; c > 0 {
					px, py := ox+x-b.Min.X, oy+y-b.Min.Y
					if image.Pt(px, py).In(f.dst.Rect) {
						f.blend(px, py, col, float32(c)/255)
					}
				}
			}
		}
	}
}

func (f *Frame) Image(img *image.RGBA, at render.Point) {
	if img == nil {
		return
	}
	o := image.Pt(f.px(at.X), f.px(at.Y))
	b := img.Rect
	area := b.Sub(b.Min).Add(o).Intersect(f.clipBounds())
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			i := img.PixOffset(b.Min.X+x-o.X, b.Min.Y+y-o.Y)
			p := img.Pix[i : i+4 : i+4]
			f.blendPremul(x, y, p[0], p[1], p[2], p[3])
		}
	}
}

func (f *Frame) Effect(kind render.EffectKind, r render.Rect, blur float32, col color.NRGBA) {
	ir := f.irect(r)
	sigma := float64(blur*f.scale) / 2
	if sigma <= 0 {
		return
	}
	pad := int(math.Ceil(3 * sigma))
	switch kind {
	case render.Glow:
		prof := func(v, lo, hi int) float64 {
			c := float64(v) + 0.5
			return bake.Phi((c-float64(lo))/sigma) - bake.Phi((c-float64(hi))/sigma)
		}
		area := ir.Inset(-pad).Intersect(f.clipBounds())
		for y := area.Min.Y; y < area.Max.Y; y++ {
			py := prof(y, ir.Min.Y, ir.Max.Y)
			for x := area.Min.X; x < area.Max.X; x++ {
				f.blend(x, y, col, float32(py*prof(x, ir.Min.X, ir.Max.X)))
			}
		}
	case render.GlowDisc:
		d := ir.Dx()
		img := bake.DiscGlow(d, pad, float32(sigma), col)
		o := image.Pt((ir.Min.X+ir.Max.X)/2-d/2-pad, (ir.Min.Y+ir.Max.Y)/2-d/2-pad)
		b := img.Rect
		area := b.Add(o).Intersect(f.clipBounds())
		for y := area.Min.Y; y < area.Max.Y; y++ {
			for x := area.Min.X; x < area.Max.X; x++ {
				c := img.NRGBAAt(x-o.X, y-o.Y)
				f.blend(x, y, color.NRGBA{R: c.R, G: c.G, B: c.B, A: 255}, float32(c.A)/255)
			}
		}
	}
}

func (f *Frame) PushClip(r render.Rect, radius float32) {
	rad := float32(0)
	if radius > 0 {
		rad = float32(f.px(min(radius, r.Dx()/2, r.Dy()/2)))
	}
	f.stack = append(f.stack, entry{clip: true, rect: f.irect(r), radius: rad})
}

func (f *Frame) PushOpacity(a float32) {
	f.stack = append(f.stack, entry{under: f.dst, alpha: a})
	f.dst = image.NewRGBA(f.dst.Rect)
}

func (f *Frame) Pop() {
	n := len(f.stack)
	if n == 0 {
		return
	}
	e := f.stack[n-1]
	f.stack = f.stack[:n-1]
	if e.clip {
		return
	}
	layer := f.dst
	f.dst = e.under
	for i := 0; i < len(layer.Pix); i += 4 {
		a := float32(layer.Pix[i+3]) * e.alpha
		if a == 0 {
			continue
		}
		p := f.dst.Pix[i : i+4 : i+4]
		inv := 1 - a/255
		p[0] = uint8(float32(layer.Pix[i])*e.alpha + float32(p[0])*inv + 0.5)
		p[1] = uint8(float32(layer.Pix[i+1])*e.alpha + float32(p[1])*inv + 0.5)
		p[2] = uint8(float32(layer.Pix[i+2])*e.alpha + float32(p[2])*inv + 0.5)
		p[3] = uint8(a + float32(p[3])*inv + 0.5)
	}
}
