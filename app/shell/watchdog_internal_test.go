package shell

import (
	"testing"
	"time"
)

// A hold-up is the machine's when the loop sat runnable while Oriel used little CPU: logged quietly, not warned of.
// Busy itself, or running, it's Oriel's.
func TestWatchdogTellsABusyMachineFromOriel(t *testing.T) {
	var used time.Duration
	w := &watchdog{cpu: func() time.Duration { return used }}
	began := time.Now().Add(-200 * time.Millisecond)
	for _, tc := range []struct {
		stack string
		used  time.Duration
		busy  bool
	}{
		{"goroutine 42 [runnable]:\nui.(*Ctx).Items", 20 * time.Millisecond, true},   // starved: 10% of a core
		{"goroutine 42 [runnable]:\nui.(*Ctx).Items", 190 * time.Millisecond, false}, // Oriel's other goroutines kept it busy
		{"goroutine 42 [running]:\nui.(*Ctx).Items", 20 * time.Millisecond, false},   // it was running its own work
	} {
		used = tc.used
		if _, busy := w.starved(tc.stack, began, 0); busy != tc.busy {
			t.Errorf("%q with %v used: busy machine %v, want %v", tc.stack[:22], tc.used, busy, tc.busy)
		}
	}
}
