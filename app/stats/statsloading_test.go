package stats_test

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	hoststats "github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/registry"
)

// The Stats page's first load is skeleton charts, never a page of zeros, with Scanning in its
// header; once the answer lands the charts draw.
func TestStatsPageLoadsWithASkeleton(t *testing.T) {
	s, r, x := statsScene(t)
	release := r.Claude.HoldScans()
	t.Cleanup(release)
	s.Run("stats.open", nil)
	s.Frame()
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.Midway(func() { s.WantFrame("Scanning…") })
	if n := s.Node("Chart"); n.Role != "status" {
		t.Errorf("the chart's placeholder reads as %+v", n)
	}
	if text := s.Text(); strings.Contains(text, "$0.00") {
		t.Errorf("the page drew zeros while loading:\n%s", text)
	}
	release()
	settle(s, x)
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	if text := s.Text(); strings.Contains(text, "Scanning…") || !strings.Contains(text, "COST") {
		t.Errorf("once loaded:\n%s", text)
	}
}

// failingStats is the session host's stats, but one session's can't be read.
type failingStats struct {
	host.StatsCalls
	id    core.SessionID
	asked atomic.Int32
}

func (f *failingStats) SessionStats(r host.StatsSessionReq) (hoststats.Detail, error) {
	if r.Session == f.id {
		f.asked.Add(1)
		return hoststats.Detail{}, errors.New("the transcript is gone")
	}
	return f.StatsCalls.SessionStats(r)
}

// A session's stats that can't be read say why, with Retry, which asks again; they never sit on a loading line.
func TestSessionStatsFailureRetries(t *testing.T) {
	s, r, x := statsScene(t)
	id := r.IDs["pricing-rounding"]
	w, err := kit.StatsCalls(s.Registry())
	if err != nil {
		t.Fatal(err)
	}
	f := &failingStats{StatsCalls: w, id: id}
	s.Registry().Contribute(registry.Service{Name: registry.HostStatsService, Impl: host.StatsCalls(f), Source: "test"})
	s.Run("stats.session", registry.Args{"id": string(id)})
	settle(s, x)
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.WantFrame("the transcript is gone", "Retry")
	before := f.asked.Load()
	s.ClickNode("Retry")
	settle(s, x)
	if f.asked.Load() == before {
		t.Error("Retry didn't ask again")
	}
	s.WantFrame("the transcript is gone", "Retry")
}

// Usage's Refresh reads the windows off the loop: the button is busy, and can't be pressed again, until it's done.
func TestUsageRefreshIsBusy(t *testing.T) {
	s, _, x := statsScene(t)
	s.Press("<D-U>")
	settle(s, x)
	s.Click(s.TextPoint("Refresh"), 0)
	s.Frame()
	s.Midway(func() { s.WantFrame("Refreshing…") })
	if x.Reading().Phase.String() != "pending" {
		t.Fatalf("after Refresh: %+v", x.Reading())
	}
	s.Until("the read", func() bool { return !strings.Contains(s.Text(), "Refreshing…") })
	if err := x.Reading().Err; err != nil {
		t.Errorf("the read failed: %v", err)
	}
}
