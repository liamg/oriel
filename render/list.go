package render

import (
	"image"

	"github.com/liamg/oriel/render/typeset"
)

type OpKind uint8

const (
	OpRect OpKind = iota
	OpPath
	OpGlyphs
	OpImage
	OpEffect
	OpPushClip
	OpPushOpacity
	OpPop
)

// Op is one recorded call on a Frame.
type Op struct {
	Kind   OpKind
	Rect   Rect // rect, effect, clip
	Style  Style
	Path   *Path
	Run    *typeset.Run
	At     Point
	Color  Color
	Image  *image.RGBA
	Effect EffectKind
	Amount float32 // effect blur, clip radius, opacity
}

// List is a recorded frame: a display list a backend replays, possibly on another goroutine.
type List struct {
	Ops   []Op
	scale float32
}

// Record returns a Frame that appends to l, at scale.
func (l *List) Record(scale float32) Frame {
	l.scale = scale
	return recorder{l}
}

// Replay draws l into f.
func (l *List) Replay(f Frame) {
	for _, op := range l.Ops {
		switch op.Kind {
		case OpRect:
			f.Rect(op.Rect, op.Style)
		case OpPath:
			f.Path(op.Path, op.Style)
		case OpGlyphs:
			f.Glyphs(op.Run, op.At, op.Color)
		case OpImage:
			f.Image(op.Image, op.At)
		case OpEffect:
			f.Effect(op.Effect, op.Rect, op.Amount, op.Color)
		case OpPushClip:
			f.PushClip(op.Rect, op.Amount)
		case OpPushOpacity:
			f.PushOpacity(op.Amount)
		case OpPop:
			f.Pop()
		}
	}
}

type recorder struct{ l *List }

func (r recorder) add(op Op) { r.l.Ops = append(r.l.Ops, op) }

func (r recorder) Rect(rc Rect, s Style) { r.add(Op{Kind: OpRect, Rect: rc, Style: s}) }
func (r recorder) Path(p *Path, s Style) { r.add(Op{Kind: OpPath, Path: p, Style: s}) }
func (r recorder) Glyphs(run *typeset.Run, at Point, c Color) {
	r.add(Op{Kind: OpGlyphs, Run: run, At: at, Color: c})
}
func (r recorder) Image(img *image.RGBA, at Point) { r.add(Op{Kind: OpImage, Image: img, At: at}) }
func (r recorder) Effect(k EffectKind, rc Rect, blur float32, c Color) {
	r.add(Op{Kind: OpEffect, Effect: k, Rect: rc, Amount: blur, Color: c})
}
func (r recorder) PushClip(rc Rect, radius float32) {
	r.add(Op{Kind: OpPushClip, Rect: rc, Amount: radius})
}
func (r recorder) PushOpacity(a float32) { r.add(Op{Kind: OpPushOpacity, Amount: a}) }
func (r recorder) Pop()                  { r.add(Op{Kind: OpPop}) }
func (r recorder) Scale() float32        { return r.l.scale }
