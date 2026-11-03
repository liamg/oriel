package kit

import (
	"testing"
	"time"
)

// A frame log's figures are over the span asked for: older frames, and the ring's overwritten ones, don't count.
func TestFrameLogStats(t *testing.T) {
	var f FrameLog
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	f.Add(now.Add(-10*time.Second), time.Second, 0, "old")
	for i := range 100 {
		lag := time.Duration(0)
		if i%2 == 0 {
			lag = time.Duration(i) * time.Millisecond
		}
		next := "spinner"
		if i%4 == 0 {
			next = "caret"
		}
		f.Add(now.Add(-time.Duration(i)*10*time.Millisecond), time.Duration(i+1)*time.Millisecond, lag, next)
	}
	s := f.Stats(now, time.Second)
	if s.Frames != 100 || s.PerSecond != 100 || s.P50 != 51*time.Millisecond || s.P99 != 100*time.Millisecond || s.Worst != 100*time.Millisecond {
		t.Errorf("stats %+v", s)
	}
	if s.Lag != 50*time.Millisecond || s.LagWorst != 98*time.Millisecond {
		t.Errorf("lag %v, worst %v", s.Lag, s.LagWorst)
	}
	if s.Waker != "spinner" || s.Wakes != 75 {
		t.Errorf("next frames asked for by %q %d times, want spinner 75", s.Waker, s.Wakes)
	}
	for range framesKept {
		f.Add(now.Add(-time.Hour), time.Millisecond, 0, "")
	}
	if s := f.Stats(now, time.Second); s.Frames != 0 {
		t.Errorf("%d frames counted past the ring", s.Frames)
	}
}
