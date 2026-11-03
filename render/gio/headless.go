package gio

import (
	"image"

	"gioui.org/gpu/headless"
	"gioui.org/op"

	"github.com/liamg/oriel/render"
)

// Headless draws frames offscreen on the GPU, for screenshots, mockup comparisons and benchmarks.
type Headless struct {
	win   *headless.Window
	size  image.Point // device pixels
	scale float32
	frame *Frame
	ops   op.Ops
}

// NewHeadless makes a w×h (logical pixels) offscreen surface at scale.
func NewHeadless(w, h int, scale float32) (*Headless, error) {
	size := image.Pt(int(float32(w)*scale+0.5), int(float32(h)*scale+0.5))
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		return nil, err
	}
	return &Headless{win: win, size: size, scale: scale, frame: NewFrame()}, nil
}

// Draw draws one frame; it returns once the GPU has the frame, not when it has finished drawing it.
func (h *Headless) Draw(fn func(render.Frame)) error {
	h.ops.Reset()
	h.frame.Begin(&h.ops, h.scale)
	fn(h.frame)
	h.frame.End()
	return h.win.Frame(&h.ops)
}

// Screenshot reads back the last frame, waiting for the GPU.
func (h *Headless) Screenshot() (*image.RGBA, error) {
	img := image.NewRGBA(image.Rectangle{Max: h.size})
	return img, h.win.Screenshot(img)
}

// Sync waits for the GPU with a 1-pixel readback.
func (h *Headless) Sync() error {
	return h.win.Screenshot(image.NewRGBA(image.Rect(0, 0, 1, 1)))
}

func (h *Headless) Release() { h.win.Release() }
