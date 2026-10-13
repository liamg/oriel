package render

import (
	"image"

	"github.com/liamg/oriel/render/typeset"
)

// Offset is f with everything drawn moved by d: a scene made for the origin, drawn into a pane.
func Offset(f Frame, d Point) Frame { return offset{f, d} }

type offset struct {
	f Frame
	d Point
}

func (o offset) pt(p Point) Point { return Point{X: p.X + o.d.X, Y: p.Y + o.d.Y} }
func (o offset) rect(r Rect) Rect { return Rect{o.pt(r.Min), o.pt(r.Max)} }

func (o offset) Rect(r Rect, s Style) { o.f.Rect(o.rect(r), s) }
func (o offset) Path(p *Path, s Style) {
	q := &Path{Segs: make([]Seg, len(p.Segs))}
	for i, sg := range p.Segs {
		for j := range sg.Pts {
			sg.Pts[j] = o.pt(sg.Pts[j])
		}
		q.Segs[i] = sg
	}
	o.f.Path(q, s)
}
func (o offset) Glyphs(run *typeset.Run, at Point, c Color) { o.f.Glyphs(run, o.pt(at), c) }
func (o offset) Image(img *image.RGBA, at Point)            { o.f.Image(img, o.pt(at)) }
func (o offset) Effect(k EffectKind, r Rect, blur float32, c Color) {
	o.f.Effect(k, o.rect(r), blur, c)
}
func (o offset) PushClip(r Rect, radius float32) { o.f.PushClip(o.rect(r), radius) }
func (o offset) PushOpacity(a float32)           { o.f.PushOpacity(a) }
func (o offset) Pop()                            { o.f.Pop() }
func (o offset) Backdrop(c Color)                { o.f.Backdrop(c) }
func (o offset) Scale() float32                  { return o.f.Scale() }
