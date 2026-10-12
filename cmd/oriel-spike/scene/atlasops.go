package scene

import (
	"image"

	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// atlasOps draws from an Atlas through Gio: one ImageOp per page, replaced only after the
// page has changed since its last upload.
type atlasOps struct {
	*Atlas
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

func newAtlasOps() *atlasOps { return &atlasOps{Atlas: NewAtlas(1024, 8)} }

func (a *atlasOps) beginFrame() {
	for i := range a.ops {
		p := &a.ops[i]
		// An op first used last frame was uploaded with everything added during that frame.
		if p.valid && p.frame == a.frame && p.gen == a.Gen && i < len(a.Pages) {
			p.version = a.Pages[i].Version
		}
	}
	a.frame++
}

func (a *atlasOps) page(i int) paint.ImageOp {
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

// draw paints entry e with its top-left at dst, clipped to clip if it's not empty.
func (a *atlasOps) draw(ops *op.Ops, e AtlasEntry, dst image.Point, cl image.Rectangle) {
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

// image returns img's entry, adding it the first time or after an eviction.
func (a *atlasOps) image(k any, img *image.NRGBA) AtlasEntry {
	if e, ok := a.Lookup(k); ok {
		return e
	}
	return a.AddImage(k, img)
}
