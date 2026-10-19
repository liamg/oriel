package app

import (
	"strings"
	"testing"
	"time"
)

// ui.md, "Loading": a Ship column shows placeholders until its project is first read, never a made-up "clean"; a
// failed read shows why with Retry; once read, the steps show, and a later read keeps them under a refresh bar.
func TestShipColumnsLoad(t *testing.T) {
	s, se := mockup18(t)
	x := s.ship()
	x.mu.Lock()
	read := x.reports[se.ID]
	pending := &shipReport{loading: true, doing: map[string]string{}, failed: map[string]string{}}
	x.reports[se.ID] = pending
	x.mu.Unlock()
	s.run("ship.open", nil)
	s.frame()
	s.now = s.now.Add(time.Second)
	s.frame()
	if n := s.access("Reading search-api"); n.Role != "status" {
		t.Errorf("the column's placeholder reads as %+v", n)
	}
	if text := s.frameText(); strings.Contains(text, "no remote") || strings.Contains(text, "Uncommitted") {
		t.Errorf("a column claims a state before it's read:\n%s", text)
	}
	for _, b := range []string{"Sync all", "Commit all"} {
		if n := s.access(b); !n.Disabled {
			t.Errorf("%s can be pressed before the projects are read", b)
		}
	}

	x.mu.Lock()
	pending.loading, pending.err = false, "gh: not logged in"
	x.mu.Unlock()
	s.now = s.now.Add(time.Second)
	s.frame()
	s.wantFrame(t, "gh: not logged in", "Retry")
	s.clickAccess(t, "Retry") // this scene has no session host: the read runs again and says so
	s.shipIdle()
	s.now = s.now.Add(time.Second)
	s.frame()
	s.wantFrame(t, "there are no sessions in this window", "Retry")

	x.mu.Lock()
	x.reports[se.ID] = read
	x.mu.Unlock()
	s.now = s.now.Add(time.Second)
	s.frame()
	s.wantFrame(t, "Uncommitted", "Behind main")
	if n := s.access("Sync all"); n.Disabled {
		t.Error("Sync all is still off once the projects are read")
	}

	x.mu.Lock()
	read.loading = true
	x.mu.Unlock()
	s.frame()
	s.now = s.now.Add(200 * time.Millisecond)
	s.frame()
	s.wantFrame(t, "Uncommitted", "Behind main")
	s.access("Reading search-api") // the refresh bar
}

// A running step shows on its own button, busy and labelled as the step; the project's other steps wait, and other
// projects' don't.
func TestShipStepButtonsShowTheirStep(t *testing.T) {
	s, se := mockup18(t)
	x := s.ship()
	s.run("ship.open", nil)
	s.settleGit(t)
	x.mu.Lock()
	x.reports[se.ID].doing["search-api"] = "Rebasing"
	x.mu.Unlock()
	s.frame()
	if n := s.access("Rebasing…"); !n.Disabled {
		t.Errorf("the busy button can be pressed: %+v", n)
	}
	if n := s.access("Commit"); !n.Disabled {
		t.Error("Commit can be pressed while the project rebases")
	}
	if n := s.access("Ready for review"); n.Disabled {
		t.Error("another project's step waits on this one's")
	}
}
