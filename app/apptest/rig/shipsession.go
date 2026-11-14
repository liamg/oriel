package rig

import (
	"testing"
	"time"

	"github.com/liamg/oriel/app/ship"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
)

// ShipSession is the Ship page's session at now: search-api with three files uncommitted, four behind and two unpushed;
// web-dashboard pushed, its draft PR #812 with five of seven checks done. reads are its projects as Ship read them,
// their origins on github.
func ShipSession(t *testing.T, now time.Time) (se core.SessionState, reads func(github *registry.CodeHost) []ship.Read) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	dirs := map[string]string{}
	for _, name := range []string{"search-api", "web-dashboard"} {
		dirs[name] = t.TempDir()
		Git(t, dirs[name], "init", "-q", "-b", "rethink/search-endpoint")
		Git(t, dirs[name], "commit", "-q", "--allow-empty", "-m", "first")
		Git(t, dirs[name], "remote", "add", "origin", "https://github.com/acme/"+name+".git")
	}
	se = core.SessionState{ID: "eng-2231", Name: "search API rate-limit headers", Kind: core.KindWork, Status: core.StatusNeedsYou,
		Ticket: "ENG-2231", Branch: "rethink/search-endpoint", StartWork: true, Started: true, Agent: core.AgentState{Provider: "codex"},
		Projects: []core.Project{{Name: "search-api", Dir: dirs["search-api"], Branch: "rethink/search-endpoint", Base: "main", Primary: true},
			{Name: "web-dashboard", Dir: dirs["web-dashboard"], Branch: "rethink/search-endpoint", Base: "main"}},
		Runs: []core.TestRun{{ID: "r1", Command: "go test ./...", OK: true, Passed: 12, Ended: now.Add(-2 * time.Minute)}}}
	for i := range 9 {
		se.Threads = append(se.Threads, core.Thread{ID: core.ThreadID("t" + string(rune('1'+i))), Status: core.ThreadResolved})
	}
	return se, func(github *registry.CodeHost) []ship.Read {
		t0 := now.Add(-4 * time.Minute)
		check := func(name, status, conclusion string, took time.Duration) registry.Check {
			c := gh.Check{Name: name, Status: status, Conclusion: conclusion, Started: t0}
			if status == "completed" {
				c.Completed = t0.Add(took)
			}
			if status == "in_progress" {
				c.Started = now.Add(-3 * time.Minute)
			}
			return ship.CheckOf(c)
		}
		return []ship.Read{
			{CodeHost: github, Repo: "acme/search-api", GitRead: now.Add(-4 * time.Second), Ship: host.Ship{Project: "search-api", Branch: "rethink/search-endpoint", Base: "main", HasOrigin: true,
				Origin: "https://github.com/acme/search-api.git",
				Files: []host.ShipFile{{Path: "internal/api/usage/handler.go", Code: "M", Add: 3, Del: 1},
					{Path: "internal/api/usage/handler_test.go", Code: "M", Add: 18}, {Path: "internal/api/usage/routes.go", Code: "M", Add: 20, Del: 5}},
				Behind: 4, Commits: 2,
				Unpushed: []host.ShipCommit{{Hash: "a41c9e2", Subject: "feat(ratelimit): expose Reservation.ResetAt"},
					{Hash: "7be0d13", Subject: "test(api): table-test the 429 path"}},
				CommitDraft: host.Draft{By: "codex", Body: "feat(api): set Retry-After on rate-limited usage responses\n\n" +
					"Both the single and batch usage endpoints now return Retry-After\nfrom the limiter's reset time, rounded up to whole seconds.\n\nRefs ENG-2231"},
				PRDraft:  host.Draft{By: "codex", Title: "feat(search): back off on 429 using Retry-After", Body: "## What\n"},
				Template: "## What\n\n## Why\n", TemplateFile: ".github/pull_request_template.md", Owners: []string{"acme/platform-team", "jkowalski"}}},
			{CodeHost: github, Repo: "acme/web-dashboard", GitRead: now.Add(-4 * time.Second), PRRead: now.Add(-28 * time.Second),
				Ship: host.Ship{Project: "web-dashboard", Branch: "rethink/search-endpoint", Base: "main", HasOrigin: true,
					Origin: "https://github.com/acme/web-dashboard.git", Commits: 3, Pushed: true, Synced: now.Add(-6 * time.Minute), Head: "c3f1a9e0b7d24e6f"},
				PR: &registry.PR{Number: 812, Title: "feat(search): back off on 429 using Retry-After", State: "open", Draft: true, Branch: "rethink/search-endpoint", Base: "main",
					Head: "c3f1a9e0b7d24e6f"},
				Checks: []registry.Check{check("lint", "completed", "success", 38*time.Second), check("typecheck", "completed", "success", 51*time.Second),
					check("unit (shard 1/2)", "completed", "success", 102*time.Second), check("unit (shard 2/2)", "completed", "success", 99*time.Second),
					check("build", "completed", "success", 125*time.Second), check("e2e · chromium", "in_progress", "", 0),
					check("preview deploy", "queued", "", 0)}},
		}
	}
}
