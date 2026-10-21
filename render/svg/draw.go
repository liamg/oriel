package svg

import (
	"math"
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

// Draw paints the document scaled into r, its text shaped by sh.
func (d *Doc) Draw(f render.Frame, r render.Rect, sh typeset.Shaper) {
	if d.view.Dx() <= 0 || d.view.Dy() <= 0 {
		return
	}
	kx, ky := r.Dx()/d.view.Dx(), r.Dy()/d.view.Dy()
	k := min(kx, ky)
	off := render.Point{X: r.Min.X + (r.Dx()-d.view.Dx()*k)/2, Y: r.Min.Y + (r.Dy()-d.view.Dy()*k)/2}
	tr := func(p render.Point) render.Point {
		return render.Point{X: off.X + (p.X-d.view.Min.X)*k, Y: off.Y + (p.Y-d.view.Min.Y)*k}
	}
	f.PushClip(r, 0)
	defer f.Pop()
	for i := range d.items {
		it := &d.items[i]
		if it.clip != nil {
			f.PushClip(render.Rect{Min: tr(it.clip.rect.Min), Max: tr(it.clip.rect.Max)}, it.clip.radius*k)
		}
		switch it.kind {
		case itemPath:
			fill := it.fill
			if it.rect != nil && fill.grad != nil && !fill.none {
				f.Rect(render.Rect{Min: tr(it.rect.Min), Max: tr(it.rect.Max)}, render.Style{Gradient: fill.grad, Radius: it.radius * k})
				fill = paint{none: true}
			}
			s := render.Style{}
			if !fill.none && fill.c.A > 0 {
				s.Fill = fill.c
			}
			if !it.stroke.none && it.stroke.c.A > 0 && it.width > 0 {
				s.Stroke, s.Width = it.stroke.c, max(it.width*k, .5)
			}
			if s.Fill.A > 0 || s.Stroke.A > 0 {
				p := &render.Path{Segs: make([]render.Seg, len(it.path.Segs))}
				for j, sg := range it.path.Segs {
					for n := range sg.Pts {
						sg.Pts[n] = tr(sg.Pts[n])
					}
					p.Segs[j] = sg
				}
				f.Path(p, s)
			}
		case itemText:
			if it.fill.none || it.fill.c.A == 0 || sh == nil {
				break
			}
			font := typeset.FontSpec{Family: family(it.family), Weight: typeset.Weight(it.weight), Size: snap(it.size * k)}
			run := sh.Shape(it.text, font)
			if it.fitTo > 0 && run.Advance > it.fitTo*k {
				font.Size = snap(font.Size * it.fitTo * k / run.Advance)
				run = sh.Shape(it.text, font)
			}
			at := tr(it.at)
			switch it.anchor {
			case "middle":
				at.X -= run.Advance / 2
			case "end":
				at.X -= run.Advance
			}
			f.Glyphs(run, at, it.fill.c)
		}
		if it.clip != nil {
			f.Pop()
		}
	}
}

// snap rounds a font size to a quarter pixel, so a few sizes are shaped rather than one per zoom step.
func snap(size float32) float32 { return max(float32(math.Round(float64(size*4)))/4, 1) }

// family is the bundled face closest to a CSS font-family list.
func family(css string) string {
	css = strings.ToLower(css)
	for _, m := range []string{"mono", "courier", "consolas", "menlo"} {
		if strings.Contains(css, m) {
			return typeset.Mono
		}
	}
	return typeset.UI
}
