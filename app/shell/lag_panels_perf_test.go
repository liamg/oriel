//go:build perf && !race

package shell_test

import (
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core/logs"
)

// logPanelScene is the log panel open over n records.
func logPanelScene(t *testing.T, n int) *apptest.Scene {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	at := time.Now()
	for i := range n {
		s.Logs.Ring.Write(logs.Record{Time: at.Add(time.Duration(i) * time.Millisecond), Level: slog.LevelInfo, Source: "core",
			Msg: fmt.Sprintf("record %d", i)})
	}
	s.Run("Log", nil)
	return s
}

// The log panel's frame doesn't grow with the records in the ring: its rows are made when one is written.
func TestLogPanelFrameWithFullRingBudget(t *testing.T) {
	f := apptest.MeanFrames(logPanelScene(t, 5), logPanelScene(t, 5000))
	few, many := f[0], f[1]
	t.Logf("the log panel's frame over 5 records: %v; over a full ring: %v", few, many)
	if many > 2*few+time.Millisecond {
		t.Errorf("a full ring makes the log panel's frame %v, against %v over 5", many, few)
	}
}
