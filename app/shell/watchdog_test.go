package shell_test

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/shell"
)

func holdTheLoop() { time.Sleep(200 * time.Millisecond) }

// A turn of the loop over 50ms logs the loop's stack while it's held up, so the culprit names itself.
func TestLoopWatchdogNamesTheCulprit(t *testing.T) {
	logs := shell.NewLogs(shell.LogOpts{})
	r := apptest.NewWindow(t, apptest.WindowOpts{Config: func(cfg *apptest.Config) { cfg.Logs = logs }})
	if err := r.Do(func() error { holdTheLoop(); return nil }); err != nil {
		t.Fatal(err)
	}
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
