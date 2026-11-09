package perf

import (
	"testing"
	"time"
)

// fakeThread is a thread clock the test moves: the reference work and each step take what the test says, times the
// load of the round they run in.
type fakeThread struct {
	now  time.Duration
	load time.Duration // how many times slower than at best everything runs now
}

func (f *fakeThread) meter() meter {
	return meter{now: func() time.Duration { return f.now }, reference: func() { f.take(10 * time.Microsecond) }, gc: func() {}}
}

func (f *fakeThread) take(d time.Duration) { f.now += d * f.load }

// A step slow in every round is reported slow; one slowed in a single round isn't; and a round run on a loaded
// machine counts what each step would take unloaded.
func TestTimesKeepsSteadySlowness(t *testing.T) {
	f := &fakeThread{}
	round := -1
	loads := []time.Duration{3, 1, 2}
	times := f.meter().times(3, 20, func() { round++; f.load = loads[round] }, func(i int) {
		switch {
		case i == 5:
			f.take(3 * time.Millisecond)
		case i == 9 && round == 1:
			f.take(3 * time.Millisecond)
		default:
			f.take(20 * time.Microsecond)
		}
	})
	if times[5] != 3*time.Millisecond {
		t.Errorf("step 5 slow every round measured %v, want 3ms", times[5])
	}
	if times[9] != 20*time.Microsecond {
		t.Errorf("step 9 slow in one round measured %v, want the 20µs of its other rounds", times[9])
	}
	if p := Pct(times, 99); p != times[5] {
		t.Errorf("p99 %v, want the slowest step's %v", p, times[5])
	}
}

// Past maxLoad a round's slowdown isn't taken off: its numbers mean nothing.
func TestTimesScalesLoadUpToMaxLoad(t *testing.T) {
	f := &fakeThread{}
	round := -1
	loads := []time.Duration{1, 8}
	times := f.meter().times(2, 2, func() { round++; f.load = loads[round] }, func(int) {
		if round == 0 {
			f.take(time.Millisecond) // more than the loaded round's, scaled, so that's the best
			return
		}
		f.take(100 * time.Microsecond)
	})
	if want := 100 * time.Microsecond * loads[1] / maxLoad; times[0] != want || times[1] != want {
		t.Errorf("steps run at %dx load measured %v, want %v: scaled by %d at most", loads[1], times, want, maxLoad)
	}
}
