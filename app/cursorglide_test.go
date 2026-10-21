package app

import (
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// FW-15: with editor.cursor_glide the cursor glides between places, and keys act at once. A step of one column, a
// move soon after the last, and the option off move it at once, so it never trails typing.
func TestCursorGlide(t *testing.T) {
	s, root := searchScene(t)
	o := s.ui.Options()
	o.ReducedMotion = false
	s.ui.SetOptions(o)
	s.now = time.Unix(1000, 0)
	s.run("editor.open", registry.Args{"path": root + "/internal/pricing/round.go"})
	s.waitHighlights()
	settle := func() {
		for range 40 {
			s.now = s.now.Add(time.Second / 120)
			s.frame()
		}
	}
	at := func() render.Point { return s.access("cursor").Bounds.Min }
	key := func(k string) {
		s.editor().e.Key(k)
		s.frame()
	}
	s.keys("3G0")
	settle()

	from := at()
	key("w")
	if got := at(); got != from {
		t.Errorf("w: the cursor jumped to %v on the first frame, want it to set off from %v", got, from)
	}
	if c := s.editor().e.Cursor(); c.Col != 5 {
		t.Errorf("w: the engine's cursor is at column %d, want 5 at once", c.Col)
	}
	s.now = s.now.Add(40 * time.Millisecond)
	s.frame()
	mid := at()
	settle()
	to := at()
	if !(mid.X > from.X && mid.X < to.X) {
		t.Errorf("w: 40 ms in the cursor is at %v, want between %v and %v", mid, from, to)
	}

	key("l")
	if got := at(); got.X <= to.X {
		t.Errorf("l: a step of one column didn't move the cursor at once: %v", got)
	}
	settle()

	key("w")
	s.now = s.now.Add(30 * time.Millisecond)
	s.frame()
	key("w")
	snapped := at()
	settle()
	if got := at(); got != snapped {
		t.Errorf("w w within 90 ms: the second didn't snap, the cursor went on from %v to %v", snapped, got)
	}

	if err := s.reg.Options().Set("editor.cursor_glide", registry.LayerUser, registry.At{}, false); err != nil {
		t.Fatal(err)
	}
	s.frame()
	before := at()
	key("b")
	if got := at(); got == before {
		t.Error("with editor.cursor_glide off, b didn't move the cursor at once")
	}
}
