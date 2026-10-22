package editor_test

import (
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// With editor.cursor_glide the cursor glides between places, and keys act at once. A step of one column, a
// move soon after the last, and the option off move it at once, so it never trails typing.
func TestCursorGlide(t *testing.T) {
	s, root := searchScene(t)
	o := s.UI().Options()
	o.ReducedMotion = false
	s.UI().SetOptions(o)
	s.Now = time.Unix(1000, 0)
	s.Run("editor.open", registry.Args{"path": root + "/internal/pricing/round.go"})
	waitHighlights(s)
	settle := func() {
		for range 40 {
			s.Now = s.Now.Add(time.Second / 120)
			s.Frame()
		}
	}
	at := func() render.Point { return s.Node("cursor").Bounds.Min }
	key := func(k string) {
		engine(s).Key(k)
		s.Frame()
	}
	keys(s, "3G0")
	settle()

	from := at()
	key("w")
	if got := at(); got != from {
		t.Errorf("w: the cursor jumped to %v on the first frame, want it to set off from %v", got, from)
	}
	if c := engine(s).Cursor(); c.Col != 5 {
		t.Errorf("w: the engine's cursor is at column %d, want 5 at once", c.Col)
	}
	s.Now = s.Now.Add(40 * time.Millisecond)
	s.Frame()
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
	s.Now = s.Now.Add(30 * time.Millisecond)
	s.Frame()
	key("w")
	snapped := at()
	settle()
	if got := at(); got != snapped {
		t.Errorf("w w within 90 ms: the second didn't snap, the cursor went on from %v to %v", snapped, got)
	}

	if err := s.Registry().Options().Set("editor.cursor_glide", registry.LayerUser, registry.At{}, false); err != nil {
		t.Fatal(err)
	}
	s.Frame()
	before := at()
	key("b")
	if got := at(); got == before {
		t.Error("with editor.cursor_glide off, b didn't move the cursor at once")
	}
}
