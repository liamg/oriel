package gio

import (
	"testing"
	"time"

	"github.com/liamg/oriel/internal/clock/clocktest"
)

// The clock reads an event's own timestamp in wall time: one that arrives late reads as late as it was, against the
// epoch the promptest one gave.
func TestInputClock(t *testing.T) {
	clk := clocktest.New(time.Unix(1_000_000, 0))
	c := inputClock{clock: clk}
	start := clk.Now()
	if at := c.at(5 * time.Second); !at.Equal(start) { // arrives as it happens
		t.Fatalf("a prompt event reads %v from now", at.Sub(start))
	}
	clk.Advance(50 * time.Millisecond)
	late := c.at(5*time.Second + 10*time.Millisecond) // happened 10 ms after the first, arriving 50 ms after it
	if behind := clk.Now().Sub(late); behind != 40*time.Millisecond {
		t.Errorf("an event 40 ms late reads %v behind", behind)
	}
	if !c.at(0).IsZero() {
		t.Error("an event with no timestamp has a time")
	}
}
