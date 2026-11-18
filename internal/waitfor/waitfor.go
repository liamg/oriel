// Package waitfor is a test's hang guard on work off its goroutine: it waits for the work's result, never for a
// while, and fails the test naming what it waited for once the wait has gone on so long the work must be stuck.
// A component with a quiescence hook (a Busy, an Idle, a Drain) is waited on through that instead.
package waitfor

import (
	"strings"
	"testing"
	"time"
)

// Timeout is how long a wait goes before it's taken as hung: long enough for a slow, loaded machine.
const Timeout = 30 * time.Second

// Poll waits for ok, asking again every millisecond, and fails the test after Timeout naming what, with what each
// of more adds about where things stand.
func Poll(t testing.TB, what string, ok func() bool, more ...func() string) {
	t.Helper()
	PollFor(t, Timeout, what, ok, more...)
}

// PollFor is Poll with a hang guard of its own.
func PollFor(t testing.TB, timeout time.Duration, what string, ok func() bool, more ...func() string) {
	t.Helper()
	for deadline := time.Now().Add(timeout); !ok(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s%s", what, details(more))
		}
	}
}

// Change waits for ok, asking again each time changed says something changed, and fails the test after Timeout
// naming what: for a source that signals its changes, so nothing is asked in between.
func Change(t testing.TB, what string, ok func() bool, changed func() <-chan struct{}) {
	t.Helper()
	timer := time.NewTimer(Timeout)
	defer timer.Stop()
	for !ok() {
		select {
		case <-changed():
		case <-timer.C:
			t.Fatalf("waiting for %s", what)
		}
	}
}

func details(more []func() string) string {
	var b strings.Builder
	for _, m := range more {
		b.WriteString("; " + m())
	}
	return b.String()
}
