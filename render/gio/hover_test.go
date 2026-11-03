package gio

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/op"

	"github.com/liamg/oriel/render"
)

// The macOS hover watcher turns the first move off the window into one far outside it: Gio must take that as the
// pointer leaving, with the cursor back to the default.
func TestMoveOffTheWindowLeaves(t *testing.T) {
	var r input.Router
	tag := new(int)
	// The window's loop reads a frame's events, then hands Gio the frame's ops.
	frame := func() {
		var ops op.Ops
		inputArea(&ops, image.Pt(800, 600), tag, render.CursorPointer)
		r.Frame(&ops)
	}
	kinds := func() (got []pointer.Kind) {
		for {
			ev, ok := r.Event(pointerFilter(tag))
			if !ok {
				return got
			}
			if pe, ok := ev.(pointer.Event); ok {
				got = append(got, pe.Kind)
			}
		}
	}
	frame()
	kinds()
	r.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(400, 300)})
	kinds()
	frame()
	if c := r.Cursor(); c != pointer.CursorPointer {
		t.Fatalf("cursor over the window = %v, want %v", c, pointer.CursorPointer)
	}
	// AppKit's y runs up; Gio's handleMouse flips it, so (-1e5, -1e5) arrives below the window.
	r.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(-1e5, 600+1e5)})
	got := kinds()
	if len(got) == 0 || got[len(got)-1] != pointer.Leave {
		t.Errorf("events after moving off = %v, want a leave", got)
	}
	if c := r.Cursor(); c != pointer.CursorDefault {
		t.Errorf("cursor off the window = %v, want %v", c, pointer.CursorDefault)
	}
}
