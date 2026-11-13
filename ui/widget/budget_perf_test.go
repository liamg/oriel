//go:build perf && !race

package widget

import (
	"testing"
	"time"

	"github.com/liamg/oriel/tools/perf"
)

// The frame budgets still hold with several things animating: a frame (build, layout and
// paint) stays under 4 ms of CPU.
func TestAnimatingFrameBudget(t *testing.T) {
	h, kick := busy(t)
	const frames = 240
	var times perf.Sampler
	for i := range frames {
		if i%20 == 0 {
			kick()
		}
		h.clock.Advance(time.Second / 120)
		times.Time(h.frame)
	}
	avg := perf.Mean(times.Times())
	t.Logf("%v a frame with six things animating", avg)
	if avg > 4*time.Millisecond {
		t.Errorf("a frame takes %v; the budget is 4 ms", avg)
	}
}
