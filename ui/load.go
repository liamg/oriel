package ui

import "time"

// Phase is where a load is.
type Phase int

const (
	Idle Phase = iota
	Pending
	Ready
	Failed
)

func (p Phase) String() string {
	return [...]string{"idle", "pending", "ready", "failed"}[p]
}

// LoadState is what a widget reads of a load, however it's fetched.
type LoadState struct {
	Phase    Phase
	Since    time.Time // when the current load started
	Err      error     // the last failure, kept while a reload runs
	Stale    bool      // Pending with an older value still shown
	Progress float64   // 0..1 when known; 0 or below when not
	Note     string    // what it's doing now: "probing codex"
}

// Loading reports whether s has nothing to show yet: not started, or on its first load.
func (s LoadState) Loading() bool { return s.Phase == Idle || s.Phase == Pending && !s.Stale }
