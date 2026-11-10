package apptest

import (
	"testing"
	"time"

	"github.com/liamg/oriel/tools/perf"
)

const (
	HostileP99 = 8 * time.Millisecond  // the hostile-data budget: a frame's p99 under it
	StallFrame = 50 * time.Millisecond // no frame, even a first one, takes longer
)

// FrameTimed skips a frame-time budget where timing means nothing.
func FrameTimed(t *testing.T) {
	t.Helper()
	if testing.Short() || testing.CoverMode() != "" {
		t.Skip("timing under -short or -cover means nothing")
	}
}

// HoldsFrameBudget times n turns of the loop, step's then the work handed to the loop and a frame, over 3 rounds
// with setup before each, and checks their p99 is under budget and none stalls (StallFrame). Times are perf.Times': the CPU each took, so tests
// running alongside can't fail it.
func (s *Scene) HoldsFrameBudget(name string, budget time.Duration, n int, setup func(), step func(i int)) {
	s.T.Helper()
	times := perf.Times(3, n, setup, func(i int) {
		step(i)
		s.RunQueued()
		s.Frame()
	})
	p50, p99, worst := perf.Pct(times, 50), perf.Pct(times, 99), perf.Pct(times, 100)
	s.T.Logf("%s: p50 %v, p99 %v, worst %v a frame over %d", name, p50, p99, worst, n)
	if p99 > budget {
		s.T.Errorf("%s: p99 %v a frame, budget %v", name, p99, budget)
	}
	if worst > StallFrame {
		s.T.Errorf("%s: a frame took %v, a stall", name, worst)
	}
}
