//go:build !race

package widget

import (
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

// busy is a frame with several things moving at once: a menu, a modal, toasts, a tab underline and a list's
// selection, and a hovered button.
func busy(t testing.TB) (*harness, func()) {
	s := &motionState{}
	for range 3 {
		s.toasts.Push(Toast{Title: "CI passed", Body: "12 checks", Tone: render.Color{R: 0x4f, G: 0xe0, B: 0xa0, A: 255}})
	}
	h := motionHarness(t.(*testing.T), false, func(c *ui.Ctx) {
		menuScene(s)(c)
		modalScene(s)(c)
		toastScene(s)(c)
		tabsScene(s)(c)
		listScene(s)(c)
	})
	s.h = h
	h.frame()
	n := 0
	kick := func() {
		n++
		s.menu, s.modal = n%2 == 0, n%3 == 0
		s.tab, s.sel = n%3, n%6
		s.toasts.Push(Toast{Title: "deploy", Body: "staging", Tone: render.Color{R: 0x4c, G: 0xc9, B: 0xf0, A: 255}})
		s.toasts.list = s.toasts.list[1:]
	}
	return h, kick
}

func BenchmarkAnimatingFrame(b *testing.B) {
	t := &testing.T{}
	h, kick := busy(t)
	b.ResetTimer()
	for i := range b.N {
		if i%20 == 0 {
			kick()
		}
		h.clock.Advance(time.Second / 120)
		h.frame()
	}
}
