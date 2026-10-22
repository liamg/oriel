package uireg

import (
	"testing"

	"github.com/liamg/oriel/core"
)

func TestFrameFor(t *testing.T) {
	r := New()
	if _, ok := FrameFor(r, Window{}); ok {
		t.Error("a frame with none contributed")
	}
	r.Contribute(Frame{ID: "home", Order: 100})
	work := r.Contribute(Frame{ID: "work", Order: 10, When: func(w Window) bool { return w.Session != nil }})
	r.Contribute(Frame{ID: "broken", Order: 0, When: func(Window) bool { panic("no") }})
	if f, _ := FrameFor(r, Window{}); f.ID != "home" {
		t.Errorf("no session: %q", f.ID)
	}
	if f, _ := FrameFor(r, Window{Session: &core.SessionState{ID: "a"}}); f.ID != "work" {
		t.Errorf("a session: %q", f.ID)
	}
	work.Dispose()
	if f, _ := FrameFor(r, Window{Session: &core.SessionState{ID: "a"}}); f.ID != "home" {
		t.Errorf("after the work frame went: %q", f.ID)
	}
}
