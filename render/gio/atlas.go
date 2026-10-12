package gio

import (
	"image"
	"image/draw"

	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"github.com/liamg/oriel/render/typeset"
)

// atlas draws from a typeset.Atlas through Gio: one ImageOp per page, replaced only after the
// page has changed since its last upload.
type atlas struct {
	*typeset.Atlas
	ops   []pageOp
	frame int
}

type pageOp struct {
	op      paint.ImageOp
	gen     int
	version int
	frame   int
	valid   bool
}

func newAtlas() *atlas { return &atlas{Atlas: typeset.NewAtlas(1024, 8)} }

func (a *atlas) beginFrame() {
	for i := range a.ops {
		p := &a.ops[i]
		// An op first used last frame was uploaded with everything added during that frame.
		if p.valid && p.frame == a.frame && p.gen == a.Gen && i < len(a.Pages) {
			p.version = a.Pages[i].Version
		}
	}
	a.frame++
}

func (a *atlas) page(i int) paint.ImageOp {
	for len(a.ops) <= i {
		a.ops = append(a.ops, pageOp{})
	}
	p := &a.ops[i]
	pg := a.Pages[i]
	if !p.valid || p.gen != a.Gen || (p.version != pg.Version && p.frame != a.frame) {
		op := paint.NewImageOp(pg.Img)
		op.Filter = paint.FilterNearest
		*p = pageOp{op: op, gen: a.Gen, version: pg.Version, frame: a.frame, valid: true}
	}
	return p.op
}

// draw paints entry e with its top-left at dst, clipped to cl if it's not empty.
func (a *atlas) draw(ops *op.Ops, e typeset.Entry, dst image.Point, cl image.Rectangle) {
	if e.Page < 0 {
		return
	}
	r := e.Src
	if !cl.Empty() {
		r = r.Intersect(cl.Sub(dst).Add(e.Src.Min))
		if r.Empty() {
			return
		}
	}
	tr := op.Offset(dst.Sub(e.Src.Min)).Push(ops)
	st := clip.Rect(r).Push(ops)
	a.page(e.Page).Add(ops)
	paint.PaintOp{}.Add(ops)
	st.Pop()
	tr.Pop()
}

// image returns img's entry, adding it the first time or after the atlas starts again.
func (a *atlas) image(k any, img *image.NRGBA) typeset.Entry {
	if e, ok := a.Lookup(k); ok {
		return e
	}
	return a.Add(k, img.Bounds().Size(), image.Point{}, func(dst *image.RGBA, at image.Point) {
		draw.Draw(dst, image.Rectangle{Min: at, Max: at.Add(img.Bounds().Size())}, img, img.Bounds().Min, draw.Src)
	})
}
