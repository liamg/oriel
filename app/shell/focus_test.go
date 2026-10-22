package shell_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// A poll keeps its pace while the window has focus, slows behind another app, and catches up when focus comes back.
func TestPollsSlowWhileUnfocused(t *testing.T) {
	f := shell.NewWindowFocus()
	var runs atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go kit.PollWith(ctx, f.Now, nil, 10*time.Millisecond, time.Hour, true, func() { runs.Add(1) })
	waitRuns := func(what string, n int32) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); runs.Load() < n; time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%s: %d runs, want %d", what, runs.Load(), n)
			}
		}
	}
	waitRuns("focused", 3)
	f.Set(false)
	time.Sleep(20 * time.Millisecond)
	was := runs.Load()
	time.Sleep(100 * time.Millisecond)
	if n := runs.Load(); n != was {
		t.Fatalf("ran %d times in 100ms behind another app", n-was)
	}
	f.Set(true)
	waitRuns("focus back", was+1)
}

// A poll at 0 doesn't run while the window hasn't focus, nor when it comes back without catchUp.
func TestSafetyPollStopsUnfocused(t *testing.T) {
	f := shell.NewWindowFocus()
	f.Set(false)
	var runs atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go kit.PollWith(ctx, f.Now, nil, 10*time.Millisecond, 0, false, func() { runs.Add(1) })
	time.Sleep(100 * time.Millisecond)
	if n := runs.Load(); n != 0 {
		t.Fatalf("ran %d times behind another app", n)
	}
	f.Set(true)
	for deadline := time.Now().Add(5 * time.Second); runs.Load() == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("never ran with focus back")
		}
	}
}

// The window losing and getting focus fires FocusLost and FocusGained, once each.
func TestFocusHooks(t *testing.T) {
	got := make(chan string, 8)
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Setup: func(h plugin.Host) {
		for _, ev := range []string{"FocusLost", "FocusGained"} {
			h.Hook(registry.Hook{Event: ev, Fn: func(registry.Ctx, any) (registry.Verdict, error) {
				got <- ev
				return registry.Verdict{}, nil
			}})
		}
	}})
	for _, f := range []bool{false, false, true} {
		r.Win.Send(render.FocusEvent{Focused: f})
	}
	for _, want := range []string{"FocusLost", "FocusGained"} {
		select {
		case ev := <-got:
			if ev != want {
				t.Fatalf("fired %s, want %s", ev, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s never fired", want)
		}
	}
	select {
	case ev := <-got:
		t.Fatalf("fired %s again", ev)
	case <-time.After(50 * time.Millisecond):
	}
}
