package widget

import (
	"testing"
	"time"

	"github.com/liamg/oriel/ui"
)

// A pulse rises from its rest to full and back, Times over, asking for the frames that move it, then rests for as
// long as it's asked; a new call pulses again. With reduced motion, or the window unfocused, it rests from the start.
func TestPulse(t *testing.T) {
	var call any = "p1"
	var got float32
	build := func(c *ui.Ctx) {
		c.Row("row", ui.Style{}, func(c *ui.Ctx) {
			got = Pulse(c, "tab", call, PulseOpts{Times: 2, Period: time.Second, Rest: 0.4})
		})
	}
	h := motionHarness(t, false, build)
	h.frame()
	if got != 0.4 {
		t.Errorf("as it starts, %v, want its rest 0.4", got)
	}
	if h.u.NextFrame().IsZero() {
		t.Error("a pulse under way asked for no frame")
	}
	h.clock.Advance(500 * time.Millisecond)
	h.frame()
	if got < 0.99 {
		t.Errorf("half a period in, %v, want full", got)
	}
	h.clock.Advance(1250 * time.Millisecond)
	h.frame()
	if got <= 0.4 || got >= 1 {
		t.Errorf("a quarter into its second pulse, %v, want between rest and full", got)
	}
	h.clock.Advance(time.Second)
	h.frame()
	if got != 0.4 {
		t.Errorf("past its pulses, %v, want its rest", got)
	}
	if next := h.u.NextFrame(); !next.IsZero() {
		t.Errorf("at rest, it asked for a frame at %v", next)
	}
	call = "p2"
	h.frame()
	h.clock.Advance(500 * time.Millisecond)
	h.frame()
	if got < 0.99 {
		t.Errorf("a new call half a period in, %v, want full", got)
	}

	still := motionHarness(t, true, build)
	still.frame()
	still.clock.Advance(500 * time.Millisecond)
	still.frame()
	if got != 0.4 {
		t.Errorf("with reduced motion, %v, want its rest", got)
	}

	away := motionHarness(t, false, build)
	away.u.SetWindow(false, true)
	away.frame()
	away.clock.Advance(500 * time.Millisecond)
	away.frame()
	if got != 0.4 {
		t.Errorf("unfocused, %v, want its rest", got)
	}
}
