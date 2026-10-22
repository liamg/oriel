package ship_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
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
		"Push + raise both PRs", "NEEDS YOU", "Uncommitted", "3 files · +41 -6", "DRAFTED BY CODEX", "Behind main", "4 commits · no conflicts predicted",
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
	s.WantFrame("Not raised", "rethink/search-endpoint → main · 2 commits", "Committed", "3 files left", "4 behind", "2 to push",
		"drafting", "Raise PR", "Ship ⇉", "RAISE PR", "Save draft", "Raise as draft", "Push + raise PR", "TITLE", "BODY", "DRAFTED BY CODEX",
		"TEMPLATE FILLED", "REVIEWERS", "CROSS-LINKED PRS", "BEFORE THIS SHIPS")
	s.Lint("the Raise PR tab")
}
