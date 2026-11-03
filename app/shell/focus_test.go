package shell_test

import (
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

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
