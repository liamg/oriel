package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/worker"
)

// shipRepo is a fixture checkout whose origin is https://github.com/acme/<name>.git, answered by a bare repo on disk
// (git's url.insteadOf), with main pushed; it returns the checkout and a second clone that moves main on.
func shipRepo(t *testing.T, name string, files map[string]string) (checkout, upstream string) {
	t.Helper()
	root := t.TempDir()
	origins := filepath.Join(root, "origins", "acme")
	checkout, upstream = filepath.Join(root, name), filepath.Join(root, "upstream-"+name)
	git(t, root, "init", "-q", "--bare", "-b", "main", filepath.Join(origins, name+".git"))
	git(t, root, "init", "-q", "-b", "main", checkout)
	writeFiles(t, checkout, files)
	git(t, checkout, "add", "-A")
	git(t, checkout, "commit", "-q", "-m", "first")
	git(t, checkout, "remote", "add", "origin", "https://github.com/acme/"+name+".git")
	git(t, checkout, "config", "url."+origins+"/.insteadOf", "https://github.com/acme/")
	git(t, checkout, "push", "-q", "origin", "main")
	git(t, checkout, "fetch", "-q", "origin")
	git(t, checkout, "remote", "set-head", "origin", "main")
	git(t, root, "clone", "-q", filepath.Join(origins, name+".git"), upstream)
	return checkout, upstream
}

// shipScene is 18's session: usage-api and web-dashboard on GitHub (a fake), a work session on both with the fake
// agent, and the window on it.
func shipScene(t *testing.T) (s *errScene, lw *localWorker, hub *ghtest.Server, se core.SessionState, ups map[string]string) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "You")
	t.Setenv("GIT_AUTHOR_EMAIL", "you@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "You")
	t.Setenv("GIT_COMMITTER_EMAIL", "you@example.com")
	hub = ghtest.New(t)
	lw = newWorkerWith(t, worker.Options{GitHub: func(string, []string) gh.Client { return hub.Client() }})
	api, apiUp := shipRepo(t, "usage-api", map[string]string{
		"internal/api/usage/handler.go":    "package usage\n\nfunc Handle() {}\n",
		"internal/api/usage/routes.go":     "package usage\n\nvar Routes = 1\n",
		".github/CODEOWNERS":               "/internal/api/  @acme/platform-team @jkowalski\n",
		".github/pull_request_template.md": "## What\n\n## Why\n",
	})
	web, webUp := shipRepo(t, "web-dashboard", map[string]string{"src/usage.ts": "export const usage = 1\n"})
	s = newSceneWith(t, "", "", mockupSize, lw.connect(t))
	waitState(t, s.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
	s.run("session.start", registry.Args{"kind": "work", "prompt": "Usage API rate-limit headers", "ticket": "INF-2231", "provider": "fake",
		"repos": []string{api, web}})
	st := waitState(t, s.store, "the session in front", func(st core.State) bool { return st.Focus != "" })
	sess, _ := st.Session(st.Focus)
	se = sess.Snapshot()
	s.reopen()
	return s, lw, hub, se, map[string]string{"usage-api": apiUp, "web-dashboard": webUp}
}

func (s *errScene) ship() *shipExt {
	for _, e := range s.exts {
		if x, ok := e.(*shipExt); ok {
			return x
		}
	}
	return nil
}

// shipSettle frames until the session's projects have been read again after every step started.
func (s *errScene) shipSettle(t *testing.T, what string, ok func() bool) {
	t.Helper()
	s.until(t, what, func() bool {
		s.ship().wait()
		s.sh.runTodo()
		s.frame()
		return ok()
	})
}

func shipDir(se core.SessionState, name string) string {
	for _, p := range se.Projects {
		if p.Name == name {
			return p.Dir
		}
	}
	return ""
}

// FG-6 done when: "flows tested against fixture repos and a fake GitHub". ui.md 18: "A flight checklist per project:
// uncommitted → behind base → unpushed → PR → checks → merge", "Multiple projects side by side, with cross-linked PRs
// and a merge order", "'Before this ships' lists the gates"; 01h: Raise PR "opens a full-width main tab … the title,
// the body editor (template-filled, drafted by the agent), reviewers, cross-linked PRs".
func TestShipFlow(t *testing.T) {
	s, lw, hub, se, ups := shipScene(t)
	x := s.ship()
	apiDir, webDir := shipDir(se, "usage-api"), shipDir(se, "web-dashboard")
	agent := lw.fake.WaitStarted(t)

	s.run("ship.open", nil)
	s.shipSettle(t, "the Ship page", func() bool {
		f := s.frameText()
		return strings.Contains(f, "SHIP") && strings.Contains(f, "usage-api") && strings.Contains(f, "web-dashboard") && strings.Contains(f, "BEFORE THIS SHIPS")
	})
	s.wantFrame(t, "INF-2231", "Plan approved", "All threads resolved", "1 open", "Tests ran this session", "AI review", "optional", "MERGE ORDER", "TICKET")

	// Uncommitted: the agent's draft fills the commit box; a file left out stays uncommitted.
	writeFiles(t, apiDir, map[string]string{
		"internal/api/usage/handler.go":      "package usage\n\nfunc Handle() { retryAfter() }\n",
		"internal/api/usage/handler_test.go": "package usage\n",
		"internal/api/usage/routes.go":       "package usage\n\nvar Routes = 2\n",
	})
	agent.Call(t, "commit_draft", map[string]any{"project": "usage-api", "message": "feat(api): set Retry-After on rate-limited usage responses"})
	s.run("ship.refresh", nil)
	s.shipSettle(t, "the uncommitted files and the draft", func() bool {
		f := s.frameText()
		return strings.Contains(f, "internal/api/usage/routes.go") && strings.Contains(f, "DRAFTED BY FAKE")
	})
	s.wantFrame(t, "Uncommitted", "3 files", "feat(api): set Retry-After on rate-limited usage responses", "NEEDS YOU")
	s.click(centre(s.access("internal/api/usage/routes.go").Bounds))
	s.click(centre(s.access("Commit").Bounds))
	s.shipSettle(t, "the commit", func() bool {
		sh, _ := x.ship(se.ID, "usage-api")
		return len(sh.Files) == 1 && len(sh.Unpushed) == 1
	})
	if got := git(t, apiDir, "log", "-1", "--format=%s%n%an"); got != "feat(api): set Retry-After on rate-limited usage responses\nYou\n" {
		t.Errorf("the commit: %q", got)
	}
	if sh, _ := x.ship(se.ID, "usage-api"); sh.Files[0].Path != "internal/api/usage/routes.go" {
		t.Errorf("the file left out was committed: %+v", sh.Files)
	}

	// A ShipCommit hook (the review gate's) stops a commit, and "Commit anyway…" goes through it with a trailer.
	gate := s.reg.Hook(registry.Hook{Event: "ShipCommit", Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
		c := ev.(*ShipCommit)
		if !c.Force {
			return registry.Verdict{Veto: true, Reason: "21 lines unreviewed"}, nil
		}
		c.Message += "\n\nUnreviewed-Agent-Lines: 21"
		return registry.Verdict{Rewrote: true}, nil
	}})
	s.typeInto(t, "Commit message", "test(api): routes")
	s.click(centre(s.access("Commit").Bounds))
	s.frame()
	s.wantFrame(t, "21 lines unreviewed", "Commit anyway…")
	s.click(centre(s.access("Commit anyway…").Bounds))
	s.shipSettle(t, "the forced commit", func() bool {
		sh, _ := x.ship(se.ID, "usage-api")
		return len(sh.Files) == 0 && len(sh.Unpushed) == 2
	})
	gate.Dispose()
	if got := git(t, apiDir, "log", "-1", "--format=%B"); !strings.Contains(got, "routes") || !strings.Contains(got, "Unreviewed-Agent-Lines: 21") {
		t.Errorf("the forced commit's message: %q", got)
	}

	// Behind its base: Rebase brings it up to date.
	writeFiles(t, ups["usage-api"], map[string]string{"README.md": "# usage-api\n"})
	git(t, ups["usage-api"], "add", "-A")
	git(t, ups["usage-api"], "commit", "-q", "-m", "docs")
	git(t, ups["usage-api"], "push", "-q", "origin", "main")
	s.run("ship.refresh", nil)
	s.shipSettle(t, "behind main", func() bool { return strings.Contains(s.frameText(), "Behind main") })
	s.wantFrame(t, "no conflicts predicted")
	s.click(centre(s.access("Rebase").Bounds))
	s.shipSettle(t, "the rebase", func() bool { sh, _ := x.ship(se.ID, "usage-api"); return sh.Behind == 0 && !sh.Synced.IsZero() })
	s.wantFrame(t, "Up to date with main", "rebased")

	// Unpushed → Push.
	s.wantFrame(t, "Unpushed", "2 commits")
	s.click(centre(s.access("Push").Bounds))
	s.shipSettle(t, "the push", func() bool { sh, _ := x.ship(se.ID, "usage-api"); return sh.Pushed && len(sh.Unpushed) == 0 })

	// Raise PR: a main tab, the agent's draft, CODEOWNERS as reviewers; raised as a draft.
	agent.Call(t, "pr_draft", map[string]any{"project": "usage-api", "title": "feat(usage): back off on 429 using Retry-After",
		"body": "## What\nRetry-After on 429s.\n\n## Why\nINF-2231.\n"})
	s.run("ship.refresh", nil)
	s.shipSettle(t, "the PR draft", func() bool { return strings.Contains(s.frameText(), "draft ready") })
	s.click(centre(s.access("Raise PR…").Bounds))
	s.until(t, "the Raise PR tab", func() bool { return strings.Contains(s.frameText(), "RAISE PR") })
	if !slices.ContainsFunc(s.visibleRefs(), func(v layout.ViewRef) bool {
		return v.ID == PRView && v.Instance == "usage-api" && v.Title == "Raise PR · usage-api"
	}) {
		t.Errorf("the tab: %+v", s.visibleRefs())
	}
	s.wantFrame(t, "TITLE", "BODY", "DRAFTED BY FAKE", "TEMPLATE FILLED", "REVIEWERS", "platform-team", "jkowalski", "CODEOWNERS",
		"CROSS-LINKED PRS", "pairs with web-dashboard", "BEFORE THIS SHIPS")
	if v := s.access("Title").Value; v != "feat(usage): back off on 429 using Retry-After" {
		t.Errorf("the title field has %q", v)
	}
	s.lint("the Raise PR tab")
	s.click(centre(s.access("Raise as draft").Bounds))
	s.shipSettle(t, "the PR", func() bool { sh, _ := x.ship(se.ID, "usage-api"); return sh.PR != nil })
	apiPRs := hub.PRs("acme/usage-api")
	if len(apiPRs) != 1 || !apiPRs[0].Draft || apiPRs[0].Title != "feat(usage): back off on 429 using Retry-After" ||
		!strings.HasPrefix(apiPRs[0].Body, "## What\nRetry-After on 429s.") || !slices.Equal(apiPRs[0].Reviewers, []string{"jkowalski", "acme/platform-team"}) {
		t.Fatalf("the PR on GitHub: %+v", apiPRs)
	}

	// web-dashboard: Commit all takes the agent's draft; Push + raise both PRs raises it and cross-links the two.
	writeFiles(t, webDir, map[string]string{"src/usage.ts": "export const usage = 2\n"})
	agent.Call(t, "commit_draft", map[string]any{"project": "web-dashboard", "message": "feat(web): back off on 429"})
	agent.Call(t, "pr_draft", map[string]any{"project": "web-dashboard", "title": "feat(web): show the wait", "body": "Shows Retry-After."})
	s.run("ship.refresh", nil)
	s.shipSettle(t, "web's draft", func() bool { sh, _ := x.ship(se.ID, "web-dashboard"); return !sh.CommitDraft.Empty() })
	s.run("ship.open", nil)
	s.frame()
	s.click(centre(s.access("Commit all").Bounds))
	s.shipSettle(t, "web committed", func() bool { sh, _ := x.ship(se.ID, "web-dashboard"); return sh.Commits == 1 && len(sh.Files) == 0 })
	s.click(centre(s.access("Push + raise both PRs").Bounds))
	s.shipSettle(t, "both PRs", func() bool {
		sh, _ := x.ship(se.ID, "web-dashboard")
		return sh.PR != nil && x.doing(se.ID, "usage-api") == "" && x.doing(se.ID, "web-dashboard") == ""
	})
	webPRs, apiPRs := hub.PRs("acme/web-dashboard"), hub.PRs("acme/usage-api")
	for _, b := range []string{apiPRs[0].Body, webPRs[0].Body} {
		if !strings.Contains(b, "1. acme/usage-api#") || !strings.Contains(b, "2. acme/web-dashboard#") || strings.Count(b, "(this PR)") != 1 {
			t.Errorf("a PR body doesn't cross-link both in merge order:\n%s", b)
		}
	}
	s.wantFrame(t, "1 · usage-api → #", "2 · web-dashboard → #", "depends on usage-api #")

	// Checks: failing CI shows on the column, the header line and the dock's checklist, with the log's line.
	t0 := time.Now().Add(-3 * time.Minute)
	hub.SetChecks("acme/web-dashboard", se.Branch,
		gh.Check{ID: 21, Name: "lint", Status: "completed", Conclusion: "success", Actions: true, Started: t0, Completed: t0.Add(38 * time.Second)},
		gh.Check{ID: 22, Name: "unit (shard 1/2)", Status: "completed", Conclusion: "failure", Actions: true, Started: t0, Completed: t0.Add(time.Minute)},
		gh.Check{ID: 23, Name: "e2e · chromium", Status: "in_progress", Actions: true, Started: t0})
	hub.SetLog(22, "2026-10-14T09:00:01.0000000Z --- FAIL: TestUsageBackoff (0.02s)\n")
	s.run("ship.refresh", nil)
	s.shipSettle(t, "the checks", func() bool { return strings.Contains(s.frameText(), "CHECKS · 1 OF 3") })
	s.wantFrame(t, "lint", "38s", "unit (shard 1/2)", "e2e · chromium", "running", "--- FAIL: TestUsageBackoff (0.02s)", "CI FAILING",
		"Fix CI with agent", "Ready for review", "Open on GitHub ↗")
	parts := registry.ProjectStateParts(s.reg, "web-dashboard", webDir)
	if len(parts) != 2 || !strings.HasPrefix(parts[0].Text, "#") || parts[1].Text != "CI failing" || !parts[1].Dot {
		t.Errorf("web-dashboard's header line: %+v", parts)
	}
	s.lint("the Ship page")

	// Ready for review takes usage-api's PR out of draft.
	s.click(centre(s.access("Ready for review").Bounds))
	s.shipSettle(t, "ready", func() bool { return !hub.PRs("acme/usage-api")[0].Draft })

	// The dock's Pull request checklist, for web-dashboard.
	s.run("project.open", registry.Args{"project": "web-dashboard"})
	s.run("project.panel", registry.Args{"name": "pull-request"})
	s.frame()
	s.wantFrame(t, "feat(web): show the wait", "Committed", "Up to date with main", "Pushed", "PR open", "Checks", "1 of 3 failing", "Edit PR")
	s.lint("the Pull request panel")

	// Fix CI with agent writes a request with the failing line attached.
	s.click(centre(s.access("Fix CI with agent").Bounds))
	s.until(t, "the request", func() bool { return strings.Contains(s.frameText(), "CI is failing on web-dashboard") })
}

// FG-6: a ShipRaise hook stops a raise (the verify quiz runs before Raise PR), its reason on the tab.
func TestShipRaiseHookStopsARaise(t *testing.T) {
	s, _, hub, se, _ := shipScene(t)
	x := s.ship()
	writeFiles(t, shipDir(se, "usage-api"), map[string]string{"x.go": "package x\n"})
	s.run("ship.refresh", nil)
	s.shipSettle(t, "the file", func() bool { sh, _ := x.ship(se.ID, "usage-api"); return len(sh.Files) == 1 })
	s.run("ship.commit", registry.Args{"project": "usage-api", "message": "feat: x"})
	s.shipSettle(t, "the commit", func() bool { sh, _ := x.ship(se.ID, "usage-api"); return sh.Commits == 1 })
	asked := 0
	s.reg.Hook(registry.Hook{Event: "ShipRaise", Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
		asked++
		if asked == 1 {
			return registry.Verdict{Veto: true, Reason: "verify the change first: the quiz runs before the PR"}, nil
		}
		return registry.Verdict{}, nil
	}})
	s.run("ship.pr.open", registry.Args{"project": "usage-api"})
	s.until(t, "the tab", func() bool { return strings.Contains(s.frameText(), "RAISE PR") })
	s.click(centre(s.access("Push + raise PR").Bounds))
	s.wantFrame(t, "verify the change first: the quiz runs before the PR")
	if len(hub.PRs("acme/usage-api")) != 0 {
		t.Fatal("a vetoed raise raised the PR")
	}
	s.click(centre(s.access("Push + raise PR").Bounds))
	s.shipSettle(t, "the PR", func() bool { sh, _ := x.ship(se.ID, "usage-api"); return sh.PR != nil })
	if got := hub.PRs("acme/usage-api"); len(got) != 1 || got[0].Title != "feat: x" || got[0].Body != "## What\n\n## Why\n" {
		t.Errorf("raised from the first commit and the template: %+v", got)
	}
	// Edit PR once it's raised.
	s.run("ship.pr.open", registry.Args{"project": "usage-api"})
	s.until(t, "Edit PR", func() bool { return strings.Contains(s.frameText(), "EDIT PR #") })
}

// RV-2 on the Ship page (ui.md 18): the commit box goes through the commit gates. A hard gate holds the commit with
// its reason and "Review N lines"; "Commit anyway…" commits with the gate's trailer and records the override in the
// session.
// ref: oracle git
func TestShipCommitGoesThroughTheCommitGates(t *testing.T) {
	s, _, _, se, _ := shipScene(t)
	x := s.ship()
	apiDir := shipDir(se, "usage-api")
	reviewed := false
	s.reg.Command(registry.Command{ID: "test.review", Title: "Review", Run: func(registry.Ctx, registry.Args) error { reviewed = true; return nil }})
	s.reg.Contribute(registry.CommitGate{Name: "fake", Label: "Agent lines reviewed", Check: func(c registry.CommitCheck) registry.GateResult {
		return registry.GateResult{Mode: registry.GateHard, Done: 1, Total: 6, Unread: 5, Reason: "5 agent-written lines unread",
			Trailer: "Unreviewed-Agent-Lines: 5", Review: "test.review", ReviewLabel: "Review 5 lines"}
	}})
	s.run("ship.open", nil)
	writeFiles(t, apiDir, map[string]string{"internal/api/usage/routes.go": "package usage\n\nvar Routes = 2\n"})
	s.run("ship.refresh", nil)
	s.shipSettle(t, "the file", func() bool { sh, _ := x.ship(se.ID, "usage-api"); return len(sh.Files) == 1 })
	s.typeInto(t, "Commit message", "feat(api): routes")
	s.click(centre(s.access("Commit").Bounds))
	s.shipSettle(t, "the gate", func() bool { return strings.Contains(s.frameText(), "Commit anyway…") })
	s.wantFrame(t, "5 agent-written lines unread", "Review 5 lines")
	if sh, _ := x.ship(se.ID, "usage-api"); len(sh.Files) != 1 {
		t.Fatal("the gate didn't hold the commit")
	}
	s.click(centre(s.access("Review 5 lines").Bounds))
	if !reviewed {
		t.Error("Review 5 lines didn't run the gate's review")
	}
	s.click(centre(s.access("Commit anyway…").Bounds))
	s.shipSettle(t, "the commit", func() bool { sh, _ := x.ship(se.ID, "usage-api"); return len(sh.Files) == 0 })
	if got := git(t, apiDir, "log", "-1", "--format=%B"); !strings.Contains(got, "feat(api): routes") || !strings.Contains(got, "Unreviewed-Agent-Lines: 5") {
		t.Errorf("the commit's message: %q", got)
	}
	waitState(t, s.store, "the override in the session", func(st core.State) bool {
		sess, ok := st.Session(se.ID)
		if !ok {
			return false
		}
		o := sess.Snapshot().Overrides
		return len(o) == 1 && o[0].Gate == "fake" && o[0].Trailer == "Unreviewed-Agent-Lines: 5" && o[0].Project == "usage-api"
	})
}
