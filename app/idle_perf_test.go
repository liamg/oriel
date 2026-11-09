//go:build perf

package app_test

import (
	"testing"
	"time"

	"github.com/liamg/oriel/app/kit"
)

// The idle budgets leave the app alone for a while on the real clock, once it's idle: they see what
// TestIdlePollsNoFasterThanASecond can't, a timer of the system's that starts a process or draws a frame.

// The idle budget: three quiet sessions, one agent working and the window behind another app
// spawn no process and run no poll faster than a second over 10s.
func TestIdleBudget(t *testing.T) {
	spawned := spawns(t)
	r := idleWindow(t)
	r.Idle() // what opening the sessions started (reading each one's git) is done
	w, ok := kit.WindowOf(r.Host)
	if !ok {
		t.Fatal("no window service")
	}
	polls := w.Polls()
	polls.Reset()
	before := len(spawned())
	time.Sleep(10 * time.Second)
	if ran := spawned()[before:]; len(ran) > 0 {
		t.Errorf("spawned %d processes while idle: %v", len(ran), ran)
	}
	if d := polls.FastestSince(); d > 0 && d < time.Second {
		t.Errorf("a poll ran every %v while idle, want a second or slower", d)
	}
}

// The idle budget's frames: at most 2 over 10s with the window behind another app.
func TestIdleFramesBudget(t *testing.T) {
	r := idleWindow(t)
	r.Idle()
	before := len(r.Win.Frames())
	time.Sleep(10 * time.Second)
	if n := len(r.Win.Frames()) - before; n > 2 {
		t.Errorf("drew %d frames while idle, want at most 2", n)
	}
}
