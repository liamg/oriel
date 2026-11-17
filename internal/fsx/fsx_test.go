package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

// In a frame, I/O through fsx panics on the loop's goroutine, in a frame-check build, and nowhere else.
func TestGuard(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	panicked := func(fn func()) (p bool) {
		defer func() { p = recover() != nil }()
		fn()
		return false
	}
	leave := Enter()
	if got := panicked(func() {
		if _, err := Stat(p); err != nil {
			t.Error(err)
		}
	}); got != Checking {
		t.Errorf("Stat in a frame panicked: %v, want %v", got, Checking)
	}
	done := make(chan bool)
	go func() {
		done <- panicked(func() {
			if _, err := ReadFile(p); err != nil {
				t.Error(err)
			}
		})
	}()
	if <-done {
		t.Error("ReadFile off the loop panicked")
	}
	leave()
	if panicked(func() {
		if _, err := Stat(p); err != nil {
			t.Error(err)
		}
	}) {
		t.Error("Stat after the frame panicked")
	}
}
