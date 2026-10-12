package render

import "github.com/liamg/oriel/render/typeset"

type Point struct{ X, Y float32 }

type Rect struct{ Min, Max Point }

// Color is non-premultiplied sRGB.
type Color struct{ R, G, B, A uint8 }

type Style struct {
	Fill Color
}

// Backend is what a renderer (Gio now, wgpu later, headless in tests) implements.
type Backend interface {
	NewWindow(o WindowOpts) (Window, error)
	Shaper() typeset.Shaper
}

type WindowOpts struct {
	Title         string
	Width, Height float32
}

type Window interface {
	Frame(fn func(f Frame)) error // draws one frame
	Events() <-chan Event         // closed when the window is gone
	Scale() float32
	Size() Point
	Close()
}

type Frame interface {
	Rect(r Rect, s Style)
	Glyphs(run *typeset.Run, at Point, c Color)
	Push(clip Rect)
	Pop()
}
