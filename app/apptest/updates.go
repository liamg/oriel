package apptest

import (
	"context"

	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/updates"
	"github.com/liamg/oriel/render/headless"
)

// Releasing is a config change that has the app update itself from r.
func Releasing(r *rig.Releases) func(*Config) {
	return func(cfg *Config) { cfg.Updates = &updates.Updates{Updater: r.Updater(), Relaunch: r.Relaunch} }
}

// CheckUpdates checks for a newer release now, as the updater's own timer does.
func (s *Scene) CheckUpdates() { PluginOf[*updates.Update](s, "update").Check() }

// WaitAfter waits for a frame drawn after the first after frames that satisfies ok, and answers its text.
func (w *Window) WaitAfter(what string, after int, ok func(string) bool) string {
	w.T.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	n := 0
	f, err := w.Win.WaitFrame(ctx, func(f headless.Frame) bool { n++; return n > after && ok(f.Text()) })
	if err != nil {
		w.T.Fatalf("no frame with %s after frame %d; last frame:\n%s", what, after, f.Text())
	}
	return f.Text()
}
