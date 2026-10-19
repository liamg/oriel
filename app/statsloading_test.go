package app

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/registry"
)

// ui.md, "Loading": the Stats page's first load is skeleton charts, never a page of zeros, with Scanning in its
// header; once the answer lands the charts draw.
func TestStatsPageLoadsWithASkeleton(t *testing.T) {
	s, _, x := statsScene(t)
	s.run("stats.open", nil)
	s.frame()
	s.now = s.now.Add(time.Second)
	s.frame()
	s.wantFrame(t, "Scanning…")
	if n := s.access("Chart"); n.Role != "status" {
		t.Errorf("the chart's placeholder reads as %+v", n)
	}
	if text := s.frameText(); strings.Contains(text, "$0.00") {
		t.Errorf("the page drew zeros while loading:\n%s", text)
	}
	s.settleStats(x)
	s.now = s.now.Add(time.Second)
	s.frame()
	if text := s.frameText(); strings.Contains(text, "Scanning…") || !strings.Contains(text, "COST") {
		t.Errorf("once loaded:\n%s", text)
	}
}

// A session's stats that can't be read say why, with Retry, which asks again; they never sit on a loading line.
func TestSessionStatsFailureRetries(t *testing.T) {
	s, r, x := statsScene(t)
	id := r.ids["pricing-rounding"]
	x.mu.Lock()
	x.details[x.detailKey(id)] = fetched[stats.Detail]{at: s.now, err: errors.New("the transcript is gone")}
	x.mu.Unlock()
	s.run("stats.session", registry.Args{"id": string(id)})
	s.now = s.now.Add(time.Second)
	s.frame()
	s.wantFrame(t, "the transcript is gone", "Retry")
	s.clickAccess(t, "Retry")
	x.mu.Lock()
	asked := len(x.inflight) > 0
	x.mu.Unlock()
	if !asked {
		t.Error("Retry didn't ask again")
	}
	s.settleStats(x)
}

// Usage's Refresh reads the windows off the loop: the button is busy, and can't be pressed again, until it's done.
func TestUsageRefreshIsBusy(t *testing.T) {
	s, _, x := statsScene(t)
	s.keyCommand(t, "<D-U>")
	s.settleStats(x)
	s.click(s.textPoint(t, "Refresh"))
	s.frame()
	s.wantFrame(t, "Refreshing…")
	if x.reading.State().Phase.String() != "pending" {
		t.Fatalf("after Refresh: %+v", x.reading.State())
	}
	s.until(t, "the read", func() bool { return !strings.Contains(s.frameText(), "Refreshing…") })
	if err := x.reading.State().Err; err != nil {
		t.Errorf("the read failed: %v", err)
	}
}

// Add a project picks a repo, says it's adding it while the host makes the worktree off the loop, and opens the
// project once it's there.
func TestAddProjectWorksOffTheLoop(t *testing.T) {
	r, _, _, lw := reviewSession(t)
	other := filepath.Join(t.TempDir(), "billing-ui")
	for _, args := range [][]string{{"init", "-q", "-b", "main", other}, {"-C", other, "-c", "user.name=x", "-c", "user.email=x@x", "commit", "-q", "--allow-empty", "-m", "first"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if _, err := lw.w.AddRepoPath(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	r.do(func() error {
		for _, p := range r.host.Pickers() {
			if p.ID == "projects.add" {
				return p.Run(context.Background(), registry.PickerItem{ID: "billing-ui"})
			}
		}
		return errors.New("no projects.add picker")
	})
	r.waitText("Adding billing-ui to the session")
	waitState(t, r.store, "billing-ui in the session", func(st core.State) bool {
		se, _ := st.Session("w")
		return hasRepo(se.Snapshot(), "billing-ui")
	})
	r.waitFrame("the notice down", func(s string) bool { return !strings.Contains(s, "Adding billing-ui") })
}
