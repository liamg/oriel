//go:build perf

package perf

import (
	"testing"
	"time"
)

// spin burns d of the thread's CPU, which is what Times measures: on a loaded machine, d of the clock is less.
func spin(d time.Duration) {
	for end := threadTime() + d; threadTime() < end; {
	}
}

// On the thread's own clock, a step slow in every round is reported slow; one slowed in a single round isn't.
func TestTimesOnTheThreadClockBudget(t *testing.T) {
	round := -1
	times := Times(3, 20, func() { round++ }, func(i int) {
		switch {
		case i == 5:
			spin(3 * time.Millisecond)
		case i == 9 && round == 1:
			spin(3 * time.Millisecond)
		}
	})
	if times[5] < 3*time.Millisecond/maxLoad {
		t.Errorf("step 5 slow every round measured %v", times[5])
	}
	if times[9] > time.Millisecond {
		t.Errorf("step 9 slow in one round measured %v", times[9])
	}
	if p := Pct(times, 99); p != times[5] {
		t.Errorf("p99 %v, want the slowest step's %v", p, times[5])
	}
}
