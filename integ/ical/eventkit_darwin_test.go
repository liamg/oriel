//go:build darwin

package ical

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// The real EventKit, on a Mac that has given this process Calendar access; without it (CI, or never asked) it skips
// rather than putting up the permission prompt.
func TestEventKitLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := EventKitArgs(14*24*time.Hour, true)
	out, err := exec.CommandContext(ctx, args[0], args[1:]...).Output()
	if err != nil {
		t.Skipf("osascript: %v", err)
	}
	if st := EventKitStatus(out); st != "ok" {
		t.Skipf("no Calendar access (%s)", st)
	}
	args = EventKitArgs(14*24*time.Hour, false)
	if out, err = exec.CommandContext(ctx, args[0], args[1:]...).Output(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	events, err := ParseEventKit(out, now)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range events {
		if !e.End.After(now) || i > 0 && e.Start.Before(events[i-1].Start) {
			t.Errorf("event %d %+v: over, or out of order", i, e)
		}
	}
	t.Logf("%d meetings in the next two weeks", len(events))
}
