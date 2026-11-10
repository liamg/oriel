package apptest

import (
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest/rig"
)

// Recv waits for ch to send, and answers what it sent; it fails the test after Timeout, naming what. It's the hang
// guard on a channel, with the harness's one timeout rather than a guess of the test's own.
func Recv[T any](t testing.TB, what string, ch <-chan T) T {
	t.Helper()
	timer := time.NewTimer(Timeout)
	defer timer.Stop()
	select {
	case v := <-ch:
		return v
	case <-timer.C:
		t.Fatalf("waiting for %s%s", what, rig.Stuck())
	}
	var zero T
	return zero
}

// Poll waits for ok, looking again every millisecond, and fails the test after Timeout, naming what. It's for what
// no scene or window draws: a file, a fake's record, a goroutine's result. A scene's own conditions are Until's.
func Poll(t testing.TB, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(Timeout); !ok(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s%s", what, rig.Stuck())
		}
	}
}
