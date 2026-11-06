package kit

import (
	"sync/atomic"
	"testing"
	"time"
)

// Work on a thing being changed waits for it to stay at one version for Wait, and the wake comes once it has.
func TestStillWaitsForAVersionToStay(t *testing.T) {
	var woken atomic.Int32
	s := Still[string]{Wait: 50 * time.Millisecond, Wake: func() { woken.Add(1) }}
	if s.Ready("buf", 1) {
		t.Fatal("ready the moment it's first seen")
	}
	time.Sleep(20 * time.Millisecond)
	if s.Ready("buf", 2) {
		t.Fatal("ready on a version just changed")
	}
	deadline := time.Now().Add(time.Second)
	for !s.Ready("buf", 2) {
		if time.Now().After(deadline) {
			t.Fatal("never ready once still")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if woken.Load() == 0 {
		t.Error("no wake once the wait was over")
	}
	var zero Still[string]
	if !zero.Ready("buf", 1) {
		t.Error("a Still with no Wait isn't ready at once")
	}
}
