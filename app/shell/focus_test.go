package shell_test

import (
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// The window losing and getting focus fires FocusLost and FocusGained, once each.
func TestFocusHooks(t *testing.T) {
	got := make(chan string, 8)
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), Setup: func(h plugin.Host) {
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
	// The last focus lost is a marker: nothing fired again before it.
	r.Win.Send(render.FocusEvent{Focused: false})
	for _, want := range []string{"FocusLost", "FocusGained", "FocusLost"} {
		if ev := apptest.Recv(t, want, got); ev != want {
			t.Fatalf("fired %s, want %s", ev, want)
		}
	}
}
