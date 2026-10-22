package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

// In a frame, I/O through fsx panics on the loop's goroutine, in a frame-check build, and nowhere else.
func TestGuard(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	os.WriteFile(p, nil, 0o644)
	panicked := func(fn func()) (p bool) {
		defer func() { p = recover() != nil }()
		fn()
		return false
	}
	leave := Enter()
	if got := panicked(func() { Stat(p) }); got != Checking {
		t.Errorf("Stat in a frame panicked: %v, want %v", got, Checking)
	}
	done := make(chan bool)
	go func() { done <- panicked(func() { ReadFile(p) }) }()
	if <-done {
		t.Error("ReadFile off the loop panicked")
	}
	leave()
	if panicked(func() { Stat(p) }) {
		t.Error("Stat after the frame panicked")
	}
}
