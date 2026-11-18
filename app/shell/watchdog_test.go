package shell_test

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/shell/diag"
	"github.com/liamg/oriel/internal/clock/clocktest"
	"github.com/liamg/oriel/internal/waitfor"
)

func holdTheLoop(held chan<- struct{}, release <-chan struct{}) {
	close(held)
	<-release
}

// parkedIn says a goroutine is blocked on a channel inside fn.
func parkedIn(fn string) bool {
	buf := make([]byte, 1<<20)
	for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
		head, _, _ := strings.Cut(g, "\n")
		if strings.Contains(head, "[chan receive") && strings.Contains(g, "."+fn+"(") {
			return true
		}
	}
	return false
}

// A turn of the loop over 50ms logs the loop's stack while it's held up, so the culprit names itself.
func TestLoopWatchdogNamesTheCulprit(t *testing.T) {
	logs := diag.NewLogs(diag.LogOpts{})
	c := clocktest.New(time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC))
	r := apptest.NewWindow(t, apptest.WindowOpts{Config: func(cfg *apptest.Config) { cfg.Logs, cfg.WatchdogClock = logs, c }})
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	r.Host.Do(func() { holdTheLoop(held, release); close(done) })
	<-held
	// Until the loop is parked on release it's runnable, and the watchdog would take a hold-up then for the machine's.
	waitfor.Poll(t, "the loop parked in holdTheLoop", func() bool { return parkedIn("holdTheLoop") })
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
