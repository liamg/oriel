package app

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
)

// mockup18 is 18's session as read: search-api with three files uncommitted, four behind and two unpushed; web-dashboard
// pushed, its draft PR #812 with five of seven checks done.
func mockup18(t *testing.T) (*errScene, core.SessionState) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	dirs := map[string]string{}
	for _, name := range []string{"search-api", "web-dashboard"} {
		dirs[name] = t.TempDir()
		git(t, dirs[name], "init", "-q", "-b", "rethink/search-endpoint")
		git(t, dirs[name], "commit", "-q", "--allow-empty", "-m", "first")
		git(t, dirs[name], "remote", "add", "origin", "https://github.com/acme/"+name+".git")
	}
	s := newScene(t, "", "", mockupSize)
	s.now = at(9, 0, 0)
	se := core.SessionState{ID: "eng-2231", Name: "search API rate-limit headers", Kind: core.KindWork, Status: core.StatusNeedsYou,
		Ticket: "ENG-2231", Branch: "rethink/search-endpoint", StartWork: true, Started: true, Agent: core.AgentState{Provider: "codex"},
		Projects: []core.Project{{Name: "search-api", Dir: dirs["search-api"], Branch: "rethink/search-endpoint", Base: "main", Primary: true},
			{Name: "web-dashboard", Dir: dirs["web-dashboard"], Branch: "rethink/search-endpoint", Base: "main"}},
		Runs: []core.TestRun{{ID: "r1", Command: "go test ./...", OK: true, Passed: 12, Ended: s.now.Add(-2 * time.Minute)}}}
	for i := range 9 {
		se.Threads = append(se.Threads, core.Thread{ID: core.ThreadID("t" + string(rune('1'+i))), Status: core.ThreadResolved})
	}
	for _, ev := range []core.Event{core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}} {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	x := s.ship()
	x.now = func() time.Time { return s.now }
	t0 := s.now.Add(-4 * time.Minute)
	check := func(name, status, conclusion string, took time.Duration) gh.Check {
		c := gh.Check{Name: name, Status: status, Conclusion: conclusion, Started: t0}
		if status == "completed" {
			c.Completed = t0.Add(took)
		}
		if status == "in_progress" {
			c.Started = s.now.Add(-3 * time.Minute)
		}
		return c
	}
	x.reports[se.ID] = &shipReport{doing: map[string]string{}, failed: map[string]string{}, ships: []host.Ship{
		{Project: "search-api", Branch: "rethink/search-endpoint", Base: "main", Remote: true, GitHub: "acme/search-api",
			Files: []host.ShipFile{{Path: "internal/api/usage/handler.go", Code: "M", Add: 3, Del: 1},
				{Path: "internal/api/usage/handler_test.go", Code: "M", Add: 18}, {Path: "internal/api/usage/routes.go", Code: "M", Add: 20, Del: 5}},
			Behind: 4, Commits: 2,
			Unpushed: []host.ShipCommit{{Hash: "a41c9e2", Subject: "feat(ratelimit): expose Reservation.ResetAt"},
				{Hash: "7be0d13", Subject: "test(api): table-test the 429 path"}},
			CommitDraft: host.Draft{By: "codex", Body: "feat(api): set Retry-After on rate-limited usage responses\n\n" +
				"Both the single and batch usage endpoints now return Retry-After\nfrom the limiter's reset time, rounded up to whole seconds.\n\nRefs ENG-2231"},
			PRDraft:  host.Draft{By: "codex", Title: "feat(search): back off on 429 using Retry-After", Body: "## What\n"},
			Template: "## What\n\n## Why\n", TemplateFile: ".github/pull_request_template.md", Owners: []string{"acme/platform-team", "jkowalski"}},
		{Project: "web-dashboard", Branch: "rethink/search-endpoint", Base: "main", Remote: true, GitHub: "acme/web-dashboard",
			Commits: 3, Pushed: true, Synced: s.now.Add(-6 * time.Minute),
			PR: &gh.PR{Number: 812, Title: "feat(search): back off on 429 using Retry-After", State: "open", Draft: true, Head: "rethink/search-endpoint", Base: "main"},
			Checks: []gh.Check{check("lint", "completed", "success", 38*time.Second), check("typecheck", "completed", "success", 51*time.Second),
				check("unit (shard 1/2)", "completed", "success", 102*time.Second), check("unit (shard 2/2)", "completed", "success", 99*time.Second),
				check("build", "completed", "success", 125*time.Second), check("e2e · chromium", "in_progress", "", 0),
				check("preview deploy", "queued", "", 0)}},
	}}
	return s, se
}

// FG-6: the Ship page against 18: two projects side by side, each step, the gates, the merge order and the ticket.
func TestShipPageSnapshot(t *testing.T) {
	s, _ := mockup18(t)
	s.run("ship.open", nil)
	s.settleGit(t)
	s.golden("ship-18")
	s.wantFrame(t, "SHIP", "ENG-2231 · search API rate-limit headers · 2 projects · branch rethink/search-endpoint", "Sync all", "Commit all",
		"Push + raise both PRs", "NEEDS YOU", "Uncommitted", "3 files · +41 -6", "DRAFTED BY CODEX", "Behind main", "4 commits · no conflicts predicted",
		"Rebase", "Unpushed", "a41c9e2", "Pull request", "not raised · draft ready", "TEMPLATE FILLED", "platform-team", "jkowalski",
		"pairs with web-dashboard", "CI RUNNING", "clean", "Up to date with main", "rebased 6m ago", "Pushed", "3 commits",
		"#812 · feat(search): back off on 429 using Retry-After", "DRAFT", "CHECKS · 5 OF 7", "1m 42s", "running 3m 00s", "queued",
		"none yet", "depends on search-api PR (not raised)", "Ready for review", "Open on GitHub ↗",
		"BEFORE THIS SHIPS", "Plan approved", "All threads resolved", "9", "Tests ran this session", "go test 2m ago",
		"1 · search-api → #—", "2 · web-dashboard → #812", "TICKET")
	s.lint("the Ship page")
	parts := registry.ProjectStateParts(s.reg, "web-dashboard", "")
	if got := []string{parts[0].Text, parts[1].Text}; strings.Join(got, " ") != "#812 CI running" {
		t.Errorf("web-dashboard's header line: %+v", parts)
	}
}

// FG-6: 42's panel 6: the Pull request checklist in the dock, and the Raise PR tab beside it.
func TestRaisePRSnapshot(t *testing.T) {
	s, _ := mockup18(t)
	s.run("ship.open", nil)
	s.settleGit(t)
	s.run("view.close", registry.Args{"id": ShipView})
	s.run("project.panel", registry.Args{"name": "pull-request"})
	s.run("ship.pr.open", registry.Args{"project": "search-api"})
	s.golden("ship-raise-pr")
	s.wantFrame(t, "Not raised", "rethink/search-endpoint → main · 2 commits", "Committed", "3 files left", "4 behind", "2 to push",
		"drafting", "Raise PR", "Ship ⇉", "RAISE PR", "Save draft", "Raise as draft", "Push + raise PR", "TITLE", "BODY", "DRAFTED BY CODEX",
		"TEMPLATE FILLED", "REVIEWERS", "CROSS-LINKED PRS", "BEFORE THIS SHIPS")
	s.lint("the Raise PR tab")
}
