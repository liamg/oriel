package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/liamg/oriel/app"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"

	"github.com/liamg/oriel/update"
)

// heldSource is a release source whose Latest waits for release.
type heldSource struct {
	update.Source
	release chan struct{}
}

func (h heldSource) Latest(ctx context.Context, channel string) (update.Release, error) {
	<-h.release
	return h.Source.Latest(ctx, channel)
}

// :UpdateCheck opens the card already checking, never saying the version running is the newest before it knows.
func TestUpdateCardIsBusyBeforeItOpens(t *testing.T) {
	r := rig.NewReleases(t)
	r.Publish("2.0.0", "")
	held := heldSource{Source: update.Dir(r.Src), release: make(chan struct{})}
	s := apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) {
		cfg.Updates = &app.Updates{Updater: r.Updater(), Relaunch: r.Relaunch}
		cfg.Updates.Updater.Source = held
	}})
	s.Now = apptest.At(14, 30, 0)
	s.Run("UpdateCheck", nil)
	s.Frame()
	if strings.Contains(s.Text(), "is the newest") {
		t.Fatal("the card said 1.0.0 is the newest before the check answered")
	}
	// The check is held open, so the app can't settle: look while it runs.
	s.Midway(func() { s.WantFrame("Checking for updates…", "Checking…") })
	s.Run("UpdateCheck", nil) // joins the check running
	close(held.release)
	s.Until("the release", func() bool { return strings.Contains(s.Text(), "Oriel 2.0.0") })
}
