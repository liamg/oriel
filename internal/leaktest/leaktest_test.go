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

// A C thread that has called into Go keeps a goroutine with no Go frames while it's back in C: the runtime's, not a
// test's.
func TestCThreadsAreNotLeaks(t *testing.T) {
	c := "goroutine 17 [syscall, locked to thread]:\nruntime.goexit({})\n\t/usr/local/go/src/runtime/asm_arm64.s:1447 +0x4\n"
	if !framework(c) {
		t.Error("a C thread's goroutine counts as a leak")
	}
	g := "goroutine 18 [chan receive]:\nexample.com/x.wait()\n\t/x/wait.go:3 +0x4\ncreated by example.com/x.Start in goroutine 1\n\t/x/x.go:9 +0x4"
	if framework(g) {
		t.Error("a goroutine a test started doesn't count")
	}
}
