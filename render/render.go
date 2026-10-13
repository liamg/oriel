package render

import (
	"image"
	"image/color"

	"github.com/liamg/oriel/render/typeset"
)

// Point and Rect are in logical (CSS) pixels; a backend scales them to the device.
type Point struct{ X, Y float32 }

type Rect struct{ Min, Max Point }

func XYWH(x, y, w, h float32) Rect { return Rect{Point{x, y}, Point{x + w, y + h}} }

func (r Rect) Dx() float32 { return r.Max.X - r.Min.X }
func (r Rect) Dy() float32 { return r.Max.Y - r.Min.Y }

// Color is non-premultiplied sRGB, as CSS writes it.
type Color = color.NRGBA

// Style is how a rect or path is painted: a fill (or gradient) and an inside stroke.
type Style struct {
	Fill     Color
	Gradient *Gradient // replaces Fill
	Radius   float32   // corner radius, for rects
	Stroke   Color
	Width    float32 // stroke width; a rect's stroke sits inside it, like a CSS border
}

// Gradient is a two-stop linear gradient across the shape's bounds.
type Gradient struct {
	From, To   Color
	Horizontal bool
}

type EffectKind int

const (
	Glow     EffectKind = iota // box-shadow: 0 0 blur colour, around a rect
	GlowDisc                   // the same around the disc inscribed in a rect
)

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
	Frame(fn func(f Frame)) error // records a frame and hands it to the backend to draw
	Events() <-chan Event         // closed when the window is gone
	Scale() float32
	Size() Point
	SetCursor(c Cursor)
	Close()
}

// Clipboard is a window's access to the system clipboard. ReadClipboard is what it held when the window last
// looked, which it does whenever it takes a key or gets focus.
type Clipboard interface {
	SetClipboard(text string)
	ReadClipboard() (string, bool)
}

// Frame is one frame's drawing. Rects and fills snap to device pixels; paths and lines don't,
// so a 0.5 px hairline stays 0.5 px.
type Frame interface {
	Rect(r Rect, s Style)
	Path(p *Path, s Style)
	Glyphs(run *typeset.Run, at Point, c Color) // at is the pen on the baseline
	Image(img *image.RGBA, at Point)            // premultiplied, device pixels, never scaled; same pointer, same pixels
	Effect(kind EffectKind, r Rect, blur float32, c Color)
	PushClip(r Rect, radius float32)
	PushOpacity(a float32) // everything until the matching Pop composites as one layer
	Pop()
	// Backdrop is what translucent fills and opacity layers sit on, for a backend that has to correct their
	// blending (Gio blends in linear light, the mockups in sRGB). It's the theme's base background.
	Backdrop(c Color)
	Scale() float32 // device pixels per logical pixel
}
