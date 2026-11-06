package clocktest

import (
	"testing"
	"time"
)

// What's scheduled runs only as the clock is moved past it, in order, at its own time; what it schedules within
// the move runs too, and a stopped one never does.
func TestAdvanceRunsWhatsDueInOrder(t *testing.T) {
	start := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	c := New(start)
	var ran []string
	var at []time.Duration
	note := func(s string) func() {
		return func() { ran, at = append(ran, s), append(at, c.Now().Sub(start)) }
	}
	c.AfterFunc(30*time.Millisecond, note("b"))
	c.AfterFunc(10*time.Millisecond, func() {
		note("a")()
		c.AfterFunc(5*time.Millisecond, note("a+5"))
	})
	stopped := c.AfterFunc(20*time.Millisecond, note("stopped"))
	if !stopped.Stop() || stopped.Stop() {
		t.Error("Stop says whether it was still waiting")
	}
	c.Advance(9 * time.Millisecond)
	if len(ran) != 0 {
		t.Fatalf("ran %v before its time", ran)
	}
	c.Advance(time.Second)
	if want := []string{"a", "a+5", "b"}; len(ran) != 3 || ran[0] != want[0] || ran[1] != want[1] || ran[2] != want[2] {
		t.Fatalf("ran %v, want %v", ran, want)
	}
	if at[0] != 10*time.Millisecond || at[1] != 15*time.Millisecond || at[2] != 30*time.Millisecond {
		t.Errorf("ran at %v", at)
	}
	if got := c.Now().Sub(start); got != 1009*time.Millisecond || c.Waiting() != 0 {
		t.Errorf("now +%v, %d waiting", got, c.Waiting())
	}
}
