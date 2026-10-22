package kit

import (
	"slices"
	"sync"
	"time"
)

// framesKept is how many frames a FrameLog holds.
const framesKept = 1024

// FrameLog is the window's last frames: when each was built, how long it took, and how far the pointer was behind
// it. Safe from any goroutine.
type FrameLog struct {
	mu     sync.Mutex
	frames []frameRec
	next   int
}

type frameRec struct {
	at        time.Time
	took, lag time.Duration
}

// Add notes a frame built at at, taking took, lag behind the oldest pointer move in it (0 with none).
func (f *FrameLog) Add(at time.Time, took, lag time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := frameRec{at, took, lag}
	if len(f.frames) < framesKept {
		f.frames = append(f.frames, r)
		return
	}
	f.frames[f.next] = r
	f.next = (f.next + 1) % framesKept
}

// FrameStats are the frames built over a span.
type FrameStats struct {
	Frames        int
	PerSecond     float64
	P50, P99      time.Duration // how long one took to build
	Worst         time.Duration
	Lag, LagWorst time.Duration // how far the pointer was behind, the median of those with a move in them
}

// Stats are the frames built in the span before now.
func (f *FrameLog) Stats(now time.Time, span time.Duration) FrameStats {
	f.mu.Lock()
	var took, lag []time.Duration
	for _, r := range f.frames {
		if d := now.Sub(r.at); d >= 0 && d <= span {
			took = append(took, r.took)
			if r.lag > 0 {
				lag = append(lag, r.lag)
			}
		}
	}
	f.mu.Unlock()
	s := FrameStats{Frames: len(took), PerSecond: float64(len(took)) / span.Seconds()}
	if len(took) == 0 {
		return s
	}
	slices.Sort(took)
	s.P50, s.P99, s.Worst = pct(took, 50), pct(took, 99), took[len(took)-1]
	if len(lag) > 0 {
		slices.Sort(lag)
		s.Lag, s.LagWorst = pct(lag, 50), lag[len(lag)-1]
	}
	return s
}

// pct is the p-th percentile of sorted.
func pct(sorted []time.Duration, p int) time.Duration {
	return sorted[min(len(sorted)-1, len(sorted)*p/100)]
}
