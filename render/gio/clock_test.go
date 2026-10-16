package gio

import (
	"testing"
	"time"
)

// The clock reads an event's own timestamp in wall time: one that arrives late reads as late as it was, against the
// epoch the promptest one gave.
func TestInputClock(t *testing.T) {
	var c inputClock
	start := time.Now()
	at := c.at(5 * time.Second) // arrives as it happens
	if d := at.Sub(start); d < -5*time.Millisecond || d > 5*time.Millisecond {
		t.Fatalf("a prompt event reads %v from now", d)
	}
	time.Sleep(50 * time.Millisecond)
	late := c.at(5*time.Second + 10*time.Millisecond) // happened 10 ms after the first, arriving 50 ms after it
	if behind := time.Since(late); behind < 35*time.Millisecond || behind > 60*time.Millisecond {
		t.Errorf("an event 40 ms late reads %v behind", behind)
	}
	if !c.at(0).IsZero() {
		t.Error("an event with no timestamp has a time")
	}
}
