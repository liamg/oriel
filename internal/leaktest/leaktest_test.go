package leaktest

import (
	"strings"
	"testing"
	"time"
)

func TestLeakedFindsAGoroutineStillRunning(t *testing.T) {
	before := map[int]bool{}
	for _, g := range goroutines() {
		before[g.id] = true
	}
	stop := make(chan struct{})
	go func() { <-stop }()
	left := Leaked(before, 50*time.Millisecond)
	close(stop)
	if len(left) != 1 || !strings.Contains(left[0], "TestLeakedFindsAGoroutineStillRunning") {
		t.Fatalf("leaked: %q", left)
	}
	if left := Leaked(before, time.Second); len(left) != 0 {
		t.Fatalf("still leaked after it ended: %q", left)
	}
}

func TestCheckPassesWhatEndsInTheGrace(t *testing.T) {
	Check(t)
	Check(t)
	stop := make(chan struct{})
	go func() { <-stop }()
	t.Cleanup(func() { time.AfterFunc(20*time.Millisecond, func() { close(stop) }) })
}
