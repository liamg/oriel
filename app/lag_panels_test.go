//go:build !race

package app

import (
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/tools/perf"
)

// meanFrame is the average of 100 frames, after 10 to settle.
func (s *errScene) meanFrame() time.Duration {
	for range 10 {
		s.frame()
	}
	start := time.Now()
	for range 100 {
		s.frame()
	}
	return time.Since(start) / 100
}

// unitsPanelFrame is the average frame with the Changed units panel open over dir's n changed files, once it's read them.
func unitsPanelFrame(t *testing.T, dir string, n int) time.Duration {
	s := newScene(t, "", dir, mockupSize)
	s.sh.git.wait()
	s.run("explain.units", nil)
	if !s.Until(30*time.Second, func() bool { _, loading := s.explainer().changedFiles(); return !loading }) {
		t.Fatalf("the Changed units panel never read %d changed files", n)
	}
	return s.meanFrame()
}

// The Changed units panel's frame doesn't grow with the files the branch changes: what it shows is worked out when
// they change, not every frame.
func TestChangedUnitsFrameWithManyChangesBudget(t *testing.T) {
	perf.Timed(t)
	few, many := unitsPanelFrame(t, changedRepo(t, 5), 5), unitsPanelFrame(t, changedRepo(t, 4000), 4000)
	t.Logf("the Changed units panel's frame over 5 changed files: %v; over 4000: %v", few, many)
	if many > 2*few+2*time.Millisecond {
		t.Errorf("4000 changed files make the Changed units panel's frame %v, against %v over 5", many, few)
	}
}

// uncommittedFrame is the average frame with the Explorer open on dir's Uncommitted scope.
func uncommittedFrame(t *testing.T, dir string) time.Duration {
	s := newScene(t, "", dir, mockupSize)
	s.sh.git.wait()
	s.keyCommand(t, "<M-2>")
	s.Until(5*time.Second, func() bool { return s.sh.git.state() != nil })
	s.sh.git.wait()
	return s.meanFrame()
}

// The Explorer's Uncommitted scope doesn't grow with the files changed: its rows are built when the status changes.
func TestExplorerUncommittedFrameWithManyChangesBudget(t *testing.T) {
	perf.Timed(t)
	few, many := uncommittedFrame(t, changedRepo(t, 5)), uncommittedFrame(t, changedRepo(t, 4000))
	t.Logf("Uncommitted's frame over 5 changed files: %v; over 4000: %v", few, many)
	if many > few+1500*time.Microsecond {
		t.Errorf("4000 uncommitted files make the Explorer's frame %v, against %v over 5", many, few)
	}
}

// logPanelFrame is the average frame with the log panel open over n records.
func logPanelFrame(t *testing.T, n int) time.Duration {
	s := newScene(t, "", t.TempDir(), mockupSize)
	at := time.Now()
	for i := range n {
		s.sh.cfg.Logs.Ring.Write(logs.Record{Time: at.Add(time.Duration(i) * time.Millisecond), Level: slog.LevelInfo, Source: "core",
			Msg: fmt.Sprintf("record %d", i)})
	}
	s.run("Log", nil)
	return s.meanFrame()
}

// The log panel's frame doesn't grow with the records in the ring: its rows are made when one is written.
func TestLogPanelFrameWithFullRingBudget(t *testing.T) {
	perf.Timed(t)
	few, many := logPanelFrame(t, 5), logPanelFrame(t, 5000)
	t.Logf("the log panel's frame over 5 records: %v; over a full ring: %v", few, many)
	if many > 2*few+time.Millisecond {
		t.Errorf("a full ring makes the log panel's frame %v, against %v over 5", many, few)
	}
}
