package scene

import (
	"image"
	"image/color"
	"image/draw"

	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// nineTile is how long the pre-stretched edge strips are. Gio adds a stencilled clip path for
// an image drawn under any scale or fractional offset, so 9-slices never scale: their pieces sit
// in the atlas, edges are tiled at integer offsets, and a uniform centre is a plain fill.
const nineTile = 256

// nine is a baked image cut for 9-slice drawing: its middle row and column stretch.
type nine struct {
	w, h   int
	pieces [9]*image.NRGBA // whole; corners TL, TR, BL, BR; edges top, bottom (nineTile wide), left, right (nineTile tall)
	centre color.NRGBA
}

type nineKey struct {
	n *nine
	i int
}

func newNine(src *image.NRGBA) *nine {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	mx, my := w/2, h/2
	n := &nine{w: w, h: h, centre: src.NRGBAAt(b.Min.X+mx, b.Min.Y+my)}
	sub := func(r image.Rectangle) *image.NRGBA {
		dst := image.NewNRGBA(image.Rectangle{Max: r.Size()})
		draw.Draw(dst, dst.Rect, src, b.Min.Add(r.Min), draw.Src)
		return dst
	}
	// A strip of the middle column (or row), repeated nineTile times along it.
	strip := func(r image.Rectangle, horizontal bool) *image.NRGBA {
		size := image.Pt(nineTile, r.Dy())
		if !horizontal {
			size = image.Pt(r.Dx(), nineTile)
		}
		dst := image.NewNRGBA(image.Rectangle{Max: size})
		for i := range nineTile {
			at := image.Pt(i, 0)
			if !horizontal {
				at = image.Pt(0, i)
			}
			draw.Draw(dst, r.Sub(r.Min).Add(at), src, b.Min.Add(r.Min), draw.Src)
		}
		return dst
	}
	n.pieces = [9]*image.NRGBA{
		src,
		sub(image.Rect(0, 0, mx, my)), sub(image.Rect(mx+1, 0, w, my)),
		sub(image.Rect(0, my+1, mx, h)), sub(image.Rect(mx+1, my+1, w, h)),
		strip(image.Rect(mx, 0, mx+1, my), true), strip(image.Rect(mx, my+1, mx+1, h), true),
		strip(image.Rect(0, my, mx, my+1), false), strip(image.Rect(mx+1, my, w, my+1), false),
	}
	return n
}

// piece draws piece i with its top-left at at, clipped to cl.
func (c *Canvas) piece(n *nine, i int, at image.Point, cl image.Rectangle) {
	if cl.Empty() {
		return
	}
	c.atlas.draw(c.Ops, c.atlas.image(nineKey{n, i}, n.pieces[i]), at, cl)
}

// drawNine paints n stretched over dst. hollow skips the centre, for borders.
func (c *Canvas) drawNine(n *nine, dst image.Rectangle, hollow bool) {
	if dst.Dx() < n.w || dst.Dy() < n.h {
		// Too small to stretch: the image as it is, clipped.
		c.piece(n, 0, dst.Min, dst)
		return
	}
	mx, my := n.w/2, n.h/2
	x0, x1, x2 := dst.Min.X, dst.Min.X+mx, dst.Max.X-(n.w-mx-1)
	y0, y1, y2 := dst.Min.Y, dst.Min.Y+my, dst.Max.Y-(n.h-my-1)
	c.piece(n, 1, image.Pt(x0, y0), image.Rect(x0, y0, x1, y1))
	c.piece(n, 2, image.Pt(x2, y0), image.Rect(x2, y0, dst.Max.X, y1))
	c.piece(n, 3, image.Pt(x0, y2), image.Rect(x0, y2, x1, dst.Max.Y))
	c.piece(n, 4, image.Pt(x2, y2), image.Rect(x2, y2, dst.Max.X, dst.Max.Y))
	c.tile(n, 5, image.Rect(x1, y0, x2, y1), true)
	c.tile(n, 6, image.Rect(x1, y2, x2, dst.Max.Y), true)
	c.tile(n, 7, image.Rect(x0, y1, x1, y2), false)
	c.tile(n, 8, image.Rect(x2, y1, dst.Max.X, y2), false)
	if hollow || n.centre.A == 0 {
		return
	}
	defer clip.Rect(image.Rect(x1, y1, x2, y2)).Push(c.Ops).Pop()
	paint.ColorOp{Color: c.col(n.centre)}.Add(c.Ops)
	paint.PaintOp{}.Add(c.Ops)
}

// tile repeats a pre-stretched strip along r.
func (c *Canvas) tile(n *nine, i int, r image.Rectangle, horizontal bool) {
	for off := 0; ; off += nineTile {
		p := r.Min.Add(image.Pt(off, 0))
		if !horizontal {
			p = r.Min.Add(image.Pt(0, off))
		}
		if !p.In(r) {
			return
		}
		c.piece(n, i, p, image.Rectangle{Min: p, Max: p.Add(n.pieces[i].Bounds().Size())}.Intersect(r))
	}
}
