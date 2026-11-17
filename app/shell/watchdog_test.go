package shell_test

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/shell/diag"
	"github.com/liamg/oriel/internal/clock/clocktest"
)

func holdTheLoop(held chan<- struct{}, release <-chan struct{}) {
	close(held)
	<-release
}

// A turn of the loop over 50ms logs the loop's stack while it's held up, so the culprit names itself.
func TestLoopWatchdogNamesTheCulprit(t *testing.T) {
	logs := diag.NewLogs(diag.LogOpts{})
	c := clocktest.New(time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC))
	r := apptest.NewWindow(t, apptest.WindowOpts{Config: func(cfg *apptest.Config) { cfg.Logs, cfg.WatchdogClock = logs, c }})
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	r.Host.Do(func() { holdTheLoop(held, release); close(done) })
	<-held
	c.Advance(time.Second)
	close(release)
	<-done
	for _, rec := range logs.Ring.Records() {
		if !strings.Contains(rec.Msg, "loop held up") {
			continue
		}
		for _, a := range rec.Attrs {
			if a.Key == "stack" && strings.Contains(a.Value, "holdTheLoop") {
				return
			}
		}
		t.Fatalf("the warning doesn't name the culprit: %+v", rec)
	}
	t.Fatal("no warning of the loop held up")
}
