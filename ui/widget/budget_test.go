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

// FW-13: "The frame budgets in rendering.md still hold with several things animating": a frame (build, layout and
// paint) stays under 4 ms of CPU.
func TestAnimatingFrameBudget(t *testing.T) {
	h, kick := busy(t)
	var total time.Duration
	const frames = 240
	for i := range frames {
		if i%20 == 0 {
			kick()
		}
		h.now = h.now.Add(time.Second / 120)
		start := time.Now()
		h.frame()
		total += time.Since(start)
	}
	avg := total / frames
	t.Logf("%v a frame with six things animating", avg)
	if avg > 4*time.Millisecond {
		t.Errorf("a frame takes %v; the budget is 4 ms", avg)
	}
}

func BenchmarkAnimatingFrame(b *testing.B) {
	t := &testing.T{}
	h, kick := busy(t)
	b.ResetTimer()
	for i := range b.N {
		if i%20 == 0 {
			kick()
		}
		h.now = h.now.Add(time.Second / 120)
		h.frame()
	}
}
