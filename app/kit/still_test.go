package kit

import (
	"testing"
	"time"

	"github.com/liamg/oriel/internal/clock/clocktest"
)

// Work on a thing being changed waits for it to stay at one version for Wait, and the wake comes once it has.
func TestStillWaitsForAVersionToStay(t *testing.T) {
	c := clocktest.New(time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC))
	woken := 0
	s := Still[string]{Wait: 50 * time.Millisecond, Wake: func() { woken++ }, Clock: c}
	if s.Ready("buf", 1) {
		t.Fatal("ready the moment it's first seen")
	}
	c.Advance(20 * time.Millisecond)
	if s.Ready("buf", 2) {
		t.Fatal("ready on a version just changed")
	}
	if !s.Held() {
		t.Error("nothing held while it waits")
	}
	c.Advance(49 * time.Millisecond)
	if s.Ready("buf", 2) {
		t.Fatal("ready before Wait was up")
	}
	c.Advance(time.Millisecond)
	if !s.Ready("buf", 2) {
		t.Fatal("not ready once still for Wait")
	}
	if woken == 0 {
		t.Error("no wake once the wait was over")
	}
	var zero Still[string]
	if !zero.Ready("buf", 1) {
		t.Error("a Still with no Wait isn't ready at once")
	}
}
