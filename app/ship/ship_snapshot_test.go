package ship_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/ship"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
)

func shipSession(t *testing.T) (*apptest.Scene, core.SessionState) { return apptest.ShipSession(t) }

// gatesCounted frames until no gate on screen is still counting (the review ledger's, read off the loop).
func gatesCounted(s *apptest.Scene, t *testing.T) {
	t.Helper()
	shipSettle(s, t, "the gates counted", func() bool { return !strings.Contains(s.Text(), "counting") })
}

// The Ship page: two projects side by side, each step, the gates, the merge order and the ticket.
func TestShipPageSnapshot(t *testing.T) {
	s, _ := shipSession(t)
	s.Run("ship.open", nil)
	s.SettleGit()
	gatesCounted(s, t)
	s.Golden("ship-18")
	s.WantFrame("SHIP", "ENG-2231 · search API rate-limit headers · 2 projects · branch rethink/search-endpoint", "Sync all", "Commit all",
		"Push + raise PR", "NEEDS YOU", "Uncommitted", "3 files · +41 -6", "DRAFTED BY CODEX", "Behind main", "4 commits · no conflicts predicted",
		"Rebase", "Unpushed", "a41c9e2", "Pull request", "not raised · draft ready", "TEMPLATE FILLED", "platform-team", "jkowalski",
		"pairs with web-dashboard", "CI RUNNING", "clean", "Up to date with main", "rebased 6m ago", "Pushed", "3 commits",
		"#812 · feat(search): back off on 429 using Retry-After", "DRAFT", "CHECKS · 5 OF 7", "1m 42s", "running 3m", "queued",
		"none yet", "depends on search-api PR (not raised)", "Ready for review", "Open on GitHub ↗",
		"BEFORE THIS SHIPS", "Plan approved", "All threads resolved", "9", "Tests ran this session", "go test 2m ago",
		"1 · search-api → #—", "2 · web-dashboard → #812", "TICKET")
	s.Lint("the Ship page")
	parts := uireg.ProjectStateParts(s.Registry(), "web-dashboard", "")
	if got := []string{parts[0].Text, parts[1].Text}; strings.Join(got, " ") != "#812 CI running" {
		t.Errorf("web-dashboard's header line: %+v", parts)
	}
}

// The Pull request checklist in the dock, and the Raise PR tab beside it.
func TestRaisePRSnapshot(t *testing.T) {
	s, _ := shipSession(t)
	s.Run("ship.open", nil)
	s.SettleGit()
	s.Run("view.close", registry.Args{"id": kit.ShipView})
	s.Run("project.panel", registry.Args{"name": "pull-request"})
	s.Run("ship.pr.open", registry.Args{"project": "search-api"})
	gatesCounted(s, t)
	s.Golden("ship-raise-pr")
	s.WantFrame("DRAFTING", "2 commits", "rethink/search-endpoint → main", "BRANCH", "Committed", "3 files left", "4 behind main", "2 to push",
		"git read 4s ago", "Raise PR", "Ship ⇉", "RAISE PR", "Save draft", "Raise as draft", "Push + raise PR", "TITLE", "BODY", "DRAFTED BY CODEX",
		"TEMPLATE FILLED", "REVIEWERS", "SHIPS WITH", "BEFORE THIS SHIPS")
	s.Lint("the Raise PR tab")
}

// The dock's Pull request panel in each state a PR is in: every line has a word, and each box says how fresh it is.
func TestPRPanelSnapshots(t *testing.T) {
	failed := func(cs []registry.Check) []registry.Check {
		cs = slices.Clone(cs)
		cs[2].State, cs[2].Detail = registry.CheckFailed, "--- FAIL: TestUsageBackoff (0.02s)"
		return cs
	}
	for _, tc := range []struct {
		name    string
		project string
		edit    func(r *ship.Read)
		want    []string
	}{
		{"not-raised", "search-api", nil, []string{"DRAFTING", "BRANCH", "3 files left", "4 behind main", "2 to push", "git read 4s ago"}},
		{"running", "web-dashboard", nil, []string{"DRAFT", "#812 · 3 commits", "2 running, 5 passed", "e2e · chromium", "queued",
			"checked 28s ago · at c3f1a9e", "No reviewers yet", "clean", "with main", "yes", "in sync", "git read 4s ago"}},
		{"failed", "web-dashboard", func(r *ship.Read) {
			r.PR.Draft, r.Checks, r.FailLine = false, failed(r.Checks), "--- FAIL: TestUsageBackoff (0.02s)"
			r.PR.Reviewers = []string{"jkowalski"}
		}, []string{"OPEN", "1 of 7 failing", "unit (shard 1/2)", "--- FAIL: TestUsageBackoff", "(0.02s)", "Fix CI with agent", "jkowalski", "waiting"}},
		{"merged", "web-dashboard", func(r *ship.Read) {
			r.PR.State, r.PR.Merged, r.Checks = "closed", true, r.Checks[:5]
		}, []string{"MERGED", "All checks passed (5)"}},
		{"no-checks", "web-dashboard", func(r *ship.Read) {
			r.PR.Draft, r.Checks, r.Head = false, nil, "0d9e8f7a6b5c4d3e"
		}, []string{"OPEN", "No checks reported", "On origin", "new commits"}},
		{"host-error", "web-dashboard", func(r *ship.Read) {
			r.HostError, r.PRRead = "rate limited", r.PRRead.Add(-90*time.Second)
		}, []string{"rate limited", "couldn't reach GitHub · 1m ago"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, se := shipSession(t)
			if tc.edit != nil {
				shipOf(s).Fixture(se.ID, false, func(rs []ship.Read) []ship.Read {
					for i := range rs {
						if rs[i].Project == tc.project {
							pr := *rs[i].PR
							rs[i].PR = &pr
							tc.edit(&rs[i])
						}
					}
					return rs
				})
			}
			s.Run("ship.open", nil)
			s.SettleGit()
			s.Run("view.close", registry.Args{"id": kit.ShipView})
			s.Run("project.open", registry.Args{"project": tc.project})
			s.Run("project.panel", registry.Args{"name": "pull-request"})
			gatesCounted(s, t)
			s.Golden("ship-pr-panel-" + tc.name)
			s.WantFrame(tc.want...)
			s.Lint("the Pull request panel")
		})
	}
}
