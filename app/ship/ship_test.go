package ship_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/ship"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui/layout"
)

// Flows against fixture repos and a fake GitHub: a flight checklist per project (uncommitted → behind base →
// unpushed → PR → checks → merge), multiple projects side by side with a merge order, and
// "Before this ships" listing the gates. Raise PR opens a full-width main tab with the title, the body editor
// (template-filled, drafted by the agent), reviewers and the PRs it ships with.
func TestShipFlow(t *testing.T) {
	s, lw, hub, se, ups := shipScene(t)
	x := shipOf(s)
	apiDir, webDir := shipDir(se, "search-api"), shipDir(se, "web-dashboard")
	agent := lw.Fake.WaitStarted(t)

	s.Run("ship.open", nil)
	shipSettle(s, t, "the Ship page", func() bool {
		f := s.Text()
		return strings.Contains(f, "SHIP") && strings.Contains(f, "search-api") && strings.Contains(f, "web-dashboard") && strings.Contains(f, "BEFORE THIS SHIPS")
	})
	s.WantFrame("ENG-2231", "Plan approved", "All threads resolved", "1 open", "Tests ran this session", "AI review", "optional", "MERGE ORDER", "TICKET")

	// Uncommitted: the agent's draft fills the commit box; a file left out stays uncommitted.
	writeFiles(t, apiDir, map[string]string{
		"internal/api/usage/handler.go":      "package usage\n\nfunc Handle() { retryAfter() }\n",
		"internal/api/usage/handler_test.go": "package usage\n",
		"internal/api/usage/routes.go":       "package usage\n\nvar Routes = 2\n",
	})
	agent.Call(t, "commit_draft", map[string]any{"project": "search-api", "message": "feat(api): set Retry-After on rate-limited usage responses"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the uncommitted files and the draft", func() bool {
		f := s.Text()
		return strings.Contains(f, "internal/api/usage/routes.go") && strings.Contains(f, "DRAFTED BY FAKE")
	})
	s.WantFrame("Uncommitted", "3 files", "feat(api): set Retry-After on rate-limited usage responses", "NEEDS YOU")
	s.ClickOn("internal/api/usage/routes.go")
	s.ClickOn("Commit")
	shipSettle(s, t, "the commit", func() bool {
		sh, _ := x.ShipOf(se.ID, "search-api")
		return len(sh.Files) == 1 && len(sh.Unpushed) == 1
	})
	if got := git(t, apiDir, "log", "-1", "--format=%s%n%an"); got != "feat(api): set Retry-After on rate-limited usage responses\nYou\n" {
		t.Errorf("the commit: %q", got)
	}
	if sh, _ := x.ShipOf(se.ID, "search-api"); sh.Files[0].Path != "internal/api/usage/routes.go" {
		t.Errorf("the file left out was committed: %+v", sh.Files)
	}

	// A ship.ShipCommit hook (the review gate's) stops a commit, and "Commit anyway…" goes through it with a trailer.
	gate := s.Registry().Hook(registry.Hook{Event: "ShipCommit", Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
		c := ev.(*ship.ShipCommit)
		if !c.Force {
			return registry.Verdict{Veto: true, Reason: "21 lines unreviewed"}, nil
		}
		c.Message += "\n\nUnreviewed-Agent-Lines: 21"
		return registry.Verdict{Rewrote: true}, nil
	}})
	s.TypeInto("Commit message", "test(api): routes")
	s.ClickOn("Commit")
	s.Frame()
	s.WantFrame("21 lines unreviewed", "Commit anyway…")
	s.ClickOn("Commit anyway…")
	shipSettle(s, t, "the forced commit", func() bool {
		sh, _ := x.ShipOf(se.ID, "search-api")
		return len(sh.Files) == 0 && len(sh.Unpushed) == 2
	})
	gate.Dispose()
	if got := git(t, apiDir, "log", "-1", "--format=%B"); !strings.Contains(got, "routes") || !strings.Contains(got, "Unreviewed-Agent-Lines: 21") {
		t.Errorf("the forced commit's message: %q", got)
	}

	// Behind its base: Rebase brings it up to date.
	writeFiles(t, ups["search-api"], map[string]string{"README.md": "# search-api\n"})
	git(t, ups["search-api"], "add", "-A")
	git(t, ups["search-api"], "commit", "-q", "-m", "docs")
	git(t, ups["search-api"], "push", "-q", "origin", "main")
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "behind main", func() bool { return strings.Contains(s.Text(), "Behind main") })
	s.WantFrame("no conflicts predicted")
	s.ClickOn("Rebase")
	shipSettle(s, t, "the rebase", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.Behind == 0 && !sh.Synced.IsZero() })
	s.WantFrame("Up to date with main", "rebased")

	// Unpushed → Push.
	s.WantFrame("Unpushed", "2 commits")
	s.ClickOn("Push")
	shipSettle(s, t, "the push", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.Pushed && len(sh.Unpushed) == 0 })

	// Raise PR: a main tab, the agent's draft, CODEOWNERS as reviewers; raised as a draft.
	agent.Call(t, "pr_draft", map[string]any{"project": "search-api", "title": "feat(search): back off on 429 using Retry-After",
		"body": "## What\nRetry-After on 429s.\n\n## Why\nINF-2231.\n"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the PR draft", func() bool { return strings.Contains(s.Text(), "draft ready") })
	s.ClickOn("Raise PR…")
	s.Until("the Raise PR tab", func() bool { return strings.Contains(s.Text(), "RAISE PR") })
	if !slices.ContainsFunc(s.Host().Layout().Visible(), func(v layout.ViewRef) bool {
		return v.ID == kit.PRView && v.Instance == "search-api"
	}) {
		t.Errorf("the tab: %+v", s.Host().Layout().Visible())
	}
	s.WantFrame("Raise PR · search-api", "TITLE", "BODY", "DRAFTED BY FAKE", "TEMPLATE FILLED", "REVIEWERS", "platform-team", "jkowalski", "CODEOWNERS",
		"SHIPS WITH", "pairs with web-dashboard", "BEFORE THIS SHIPS")
	if v := s.Node("Title").Value; v != "feat(search): back off on 429 using Retry-After" {
		t.Errorf("the title field has %q", v)
	}
	s.Lint("the Raise PR tab")
	s.ClickOn("Raise as draft")
	shipSettle(s, t, "the PR", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.PR != nil })
	apiPRs := hub.PRs("acme/search-api")
	if len(apiPRs) != 1 || !apiPRs[0].Draft || apiPRs[0].Title != "feat(search): back off on 429 using Retry-After" ||
		!strings.HasPrefix(apiPRs[0].Body, "## What\nRetry-After on 429s.") || !slices.Equal(apiPRs[0].Reviewers, []string{"jkowalski", "acme/platform-team"}) {
		t.Fatalf("the PR on GitHub: %+v", apiPRs)
	}

	// web-dashboard: Commit all takes the agent's draft; Push + raise PR raises it. The descriptions are left as written:
	// the merge order is the Ship page's, not a block in each.
	writeFiles(t, webDir, map[string]string{"src/usage.ts": "export const usage = 2\n"})
	agent.Call(t, "commit_draft", map[string]any{"project": "web-dashboard", "message": "feat(web): back off on 429"})
	agent.Call(t, "pr_draft", map[string]any{"project": "web-dashboard", "title": "feat(web): show the wait", "body": "Shows Retry-After."})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "web's draft", func() bool { sh, _ := x.ShipOf(se.ID, "web-dashboard"); return !sh.CommitDraft.Empty() })
	s.Run("ship.open", nil)
	s.Frame()
	s.ClickOn("Commit all")
	shipSettle(s, t, "web committed", func() bool { sh, _ := x.ShipOf(se.ID, "web-dashboard"); return sh.Commits == 1 && len(sh.Files) == 0 })
	s.ClickOn("Push + raise PR")
	shipSettle(s, t, "both PRs", func() bool {
		sh, _ := x.ShipOf(se.ID, "web-dashboard")
		return sh.PR != nil && x.Doing(se.ID, "search-api") == "" && x.Doing(se.ID, "web-dashboard") == ""
	})
	webPRs, apiPRs := hub.PRs("acme/web-dashboard"), hub.PRs("acme/search-api")
	for _, b := range []string{apiPRs[0].Body, webPRs[0].Body} {
		if strings.Contains(b, "oriel:linked-prs") || strings.Contains(b, "Ships with") || strings.Contains(b, "acme/web-dashboard#") {
			t.Errorf("a PR body was given a block linking the session's PRs:\n%s", b)
		}
	}
	if webPRs[0].Body != "Shows Retry-After." {
		t.Errorf("web's PR body is %q, not the draft as written", webPRs[0].Body)
	}
	s.WantFrame("1 · search-api → #", "2 · web-dashboard → #", "depends on search-api #")

	// Checks: failing CI shows on the column, the header line and the dock's checklist, with the log's line.
	t0 := time.Now().Add(-3 * time.Minute)
	hub.SetChecks("acme/web-dashboard", se.Branch,
		gh.Check{ID: 21, Name: "lint", Status: "completed", Conclusion: "success", Actions: true, Started: t0, Completed: t0.Add(38 * time.Second)},
		gh.Check{ID: 22, Name: "unit (shard 1/2)", Status: "completed", Conclusion: "failure", Actions: true, Started: t0, Completed: t0.Add(time.Minute)},
		gh.Check{ID: 23, Name: "e2e · chromium", Status: "in_progress", Actions: true, Started: t0})
	hub.SetLog(22, "2026-10-14T09:00:01.0000000Z --- FAIL: TestUsageBackoff (0.02s)\n")
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the checks", func() bool { return strings.Contains(s.Text(), "CHECKS · 1 OF 3") })
	s.WantFrame("lint", "38s", "unit (shard 1/2)", "e2e · chromium", "running", "--- FAIL: TestUsageBackoff (0.02s)", "CI FAILING",
		"Fix CI with agent", "Ready for review", "Open on GitHub ↗")
	parts := uireg.ProjectStateParts(s.Registry(), "web-dashboard", webDir)
	if len(parts) != 2 || !strings.HasPrefix(parts[0].Text, "#") || parts[1].Text != "CI failing" || !parts[1].Dot {
		t.Errorf("web-dashboard's header line: %+v", parts)
	}
	s.Lint("the Ship page")

	// Ready for review takes search-api's PR out of draft.
	s.ClickOn("Ready for review")
	shipSettle(s, t, "ready", func() bool { return !hub.PRs("acme/search-api")[0].Draft })

	// The dock's Pull request checklist, for web-dashboard.
	s.Run("project.open", registry.Args{"project": "web-dashboard"})
	s.Run("project.panel", registry.Args{"name": "pull-request"})
	s.Frame()
	s.WantFrame("feat(web): show the wait", "OPEN", "CHECKS", "1 of 3 failing", "unit (shard 1/2)", "e2e · chromium", "checked", "REVIEWS",
		"BRANCH", "Committed", "clean", "Up to date", "with main", "Pushed", "yes", "git read", "View PR")
	s.Lint("the Pull request panel")

	// Fix CI with agent writes a request with the failing line attached.
	s.ClickOn("Fix CI with agent")
	s.Until("the request", func() bool { return strings.Contains(s.Text(), "CI is failing on web-dashboard") })

	// A PR closed on GitHub reads as closed, not open, and can still be opened.
	hub.ClosePR("acme/web-dashboard", webPRs[0].Number)
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the closed PR", func() bool { return strings.Contains(s.Text(), "CLOSED") })
	s.WantFrame("CLOSED", "View PR")
	for line := range strings.Lines(s.Text()) {
		if strings.TrimSpace(line) == "OPEN" {
			t.Error("the checklist still says the closed PR is open")
		}
	}
	s.ClickOn("View PR")
	s.Until("the closed PR's overview", func() bool { return strings.Contains(s.Text(), "Raise a new PR…") })
}

// A ShipRaise hook stops a raise (the verify quiz runs before Raise PR), its reason on the tab.
func TestShipRaiseHookStopsARaise(t *testing.T) {
	s, _, hub, se, _ := shipScene(t)
	x := shipOf(s)
	writeFiles(t, shipDir(se, "search-api"), map[string]string{"x.go": "package x\n"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the file", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return len(sh.Files) == 1 })
	s.Run("ship.commit", registry.Args{"project": "search-api", "message": "feat: x\n\nAdds x."})
	shipSettle(s, t, "the commit", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.Commits == 1 })
	asked := 0
	s.Registry().Hook(registry.Hook{Event: "ShipRaise", Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
		asked++
		if asked == 1 {
			return registry.Verdict{Veto: true, Reason: "verify the change first: the quiz runs before the PR"}, nil
		}
		return registry.Verdict{}, nil
	}})
	s.Run("ship.pr.open", registry.Args{"project": "search-api"})
	s.Until("the tab", func() bool { return strings.Contains(s.Text(), "RAISE PR") })
	s.ClickOn("Push + raise PR")
	s.WantFrame("verify the change first: the quiz runs before the PR")
	if len(hub.PRs("acme/search-api")) != 0 {
		t.Fatal("a vetoed raise raised the PR")
	}
	s.ClickOn("Push + raise PR")
	shipSettle(s, t, "the PR", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.PR != nil })
	if got := hub.PRs("acme/search-api"); len(got) != 1 || got[0].Title != "feat: x" || got[0].Body != "Adds x.\n\n## What\n\n## Why\n" {
		t.Errorf("raised from the first commit's message and the template: %+v", got)
	}
	// Edit PR once it's raised.
	s.Run("ship.pr.open", registry.Args{"project": "search-api", "edit": true})
	s.Until("Edit PR", func() bool { return strings.Contains(s.Text(), "EDIT PR #") })
}

// On the Ship page the commit box goes through the commit gates. A hard gate holds the commit with
// its reason and "Review N lines"; "Commit anyway…" commits with the gate's trailer and records the override in the
// session.
// ref: oracle git
func TestShipCommitGoesThroughTheCommitGates(t *testing.T) {
	s, _, _, se, _ := shipScene(t)
	x := shipOf(s)
	apiDir := shipDir(se, "search-api")
	reviewed := false
	s.Registry().Command(registry.Command{ID: "test.review", Title: "Review", Run: func(registry.Ctx, registry.Args) error { reviewed = true; return nil }})
	s.Registry().Contribute(registry.CommitGate{Name: "fake", Label: "Agent lines reviewed", Check: func(c registry.CommitCheck) registry.GateResult {
		return registry.GateResult{Mode: registry.GateHard, Done: 1, Total: 6, Unread: 5, Reason: "5 agent-written lines unread",
			Trailer: "Unreviewed-Agent-Lines: 5", Review: "test.review", ReviewLabel: "Review 5 lines"}
	}})
	s.Run("ship.open", nil)
	writeFiles(t, apiDir, map[string]string{"internal/api/usage/routes.go": "package usage\n\nvar Routes = 2\n"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the file", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return len(sh.Files) == 1 })
	s.TypeInto("Commit message", "feat(api): routes")
	s.ClickOn("Commit")
	shipSettle(s, t, "the gate", func() bool { return strings.Contains(s.Text(), "Commit anyway…") })
	s.WantFrame("5 agent-written lines unread", "Review 5 lines")
	if sh, _ := x.ShipOf(se.ID, "search-api"); len(sh.Files) != 1 {
		t.Fatal("the gate didn't hold the commit")
	}
	s.ClickOn("Review 5 lines")
	if !reviewed {
		t.Error("Review 5 lines didn't run the gate's review")
	}
	s.ClickOn("Commit anyway…")
	shipSettle(s, t, "the commit", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return len(sh.Files) == 0 })
	if got := git(t, apiDir, "log", "-1", "--format=%B"); !strings.Contains(got, "feat(api): routes") || !strings.Contains(got, "Unreviewed-Agent-Lines: 5") {
		t.Errorf("the commit's message: %q", got)
	}
	s.UntilState("the override in the session", func(st core.State) bool {
		sess, ok := st.Session(se.ID)
		if !ok {
			return false
		}
		o := sess.Snapshot().Overrides
		return len(o) == 1 && o[0].Gate == "fake" && o[0].Trailer == "Unreviewed-Agent-Lines: 5" && o[0].Project == "search-api"
	})
}

// The commit box says who drafted it and whether the code has moved on since ("drafted by fake · stale"); what
// the user types is kept on the host as theirs, which the agent's tool leaves, and Redraft hands it back.
func TestShipDraftsFreshnessAndYours(t *testing.T) {
	s, lw, _, se, _ := shipScene(t)
	x := shipOf(s)
	apiDir := shipDir(se, "search-api")
	agent := lw.Fake.WaitStarted(t)
	s.Run("ship.open", nil)
	writeFiles(t, apiDir, map[string]string{"internal/api/usage/routes.go": "package usage\n\nvar Routes = 2\n"})
	agent.Call(t, "commit_draft", map[string]any{"project": "search-api", "message": "feat(api): two routes"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the draft", func() bool { return strings.Contains(s.Text(), "DRAFTED BY FAKE") })
	if strings.Contains(s.Text(), "STALE") {
		t.Fatal("a fresh draft is marked stale")
	}

	writeFiles(t, apiDir, map[string]string{"internal/api/usage/routes.go": "package usage\n\nvar Routes = 3\n"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the stale draft", func() bool { return strings.Contains(s.Text(), "DRAFTED BY FAKE · STALE") })

	// Typing claims the draft at once, and keeps the whole message once the typing rests.
	s.TypeInto("Commit message", " (3)")
	onHost := func() host.Draft {
		ss, _ := lw.W.ShipRead(t.Context(), host.ShipReq{Session: se.ID})
		for _, sh := range ss {
			if sh.Project == "search-api" {
				return sh.CommitDraft
			}
		}
		return host.Draft{}
	}
	shipSettle(s, t, "the claim", func() bool { return onHost().By == host.DraftByYou })
	if d := onHost(); d.Body == "feat(api): two routes (3)" {
		t.Errorf("kept before the typing rested: %+v", d)
	}
	s.Now = s.Now.Add(ship.DraftSaveAfter)
	s.Frame()
	shipSettle(s, t, "the whole message kept", func() bool { return onHost().Body == "feat(api): two routes (3)" })
	if out := agent.Call(t, "commit_draft", map[string]any{"project": "search-api", "message": "feat(api): three routes"}); !strings.Contains(out, "the user wrote") {
		t.Errorf("the agent's tool overwrote the user's draft: %q", out)
	}
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the read", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.CommitDraft.By == host.DraftByYou })
	if v := s.Node("Commit message").Value; v != "feat(api): two routes (3)" {
		t.Errorf("the box has %q", v)
	}

	// Redraft gives the box back to the agent.
	agent.WaitSent(t) // the session's prompt
	agent.Emit(agentpkg.TurnStart{})
	rig.Answered(t, s.Store(), agent)
	agent.Emit(agentpkg.TurnEnd{})
	s.UntilState("the agent idle", func(st core.State) bool {
		sess, _ := st.Session(se.ID)
		a := sess.Snapshot().Agent
		return a.Stats.Turns == 1 && !a.Working
	})
	// It sends the redraft prompt as the user has it on the Prompts page.
	if err := (prompts.Store{Dir: s.ConfigDir}).Set("drafts.commit.again", "Once more for {{project}}: mcp__oriel__commit_draft."); err != nil {
		t.Fatal(err)
	}
	s.ClickOn("Redraft")
	if got := agent.WaitSent(t); got.Text != "Once more for search-api: mcp__oriel__commit_draft." {
		t.Errorf("Redraft sent %q", got.Text)
	}
	if out := agent.Call(t, "commit_draft", map[string]any{"project": "search-api", "message": "feat(api): three routes"}); out != "saved" {
		t.Errorf("after Redraft the tool said %q", out)
	}
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the redraft", func() bool { return strings.Contains(s.Text(), "feat(api): three routes") })
	s.WantFrame("DRAFTED BY FAKE")
}

// A draft saved after the code last changed fills the commit box with no refresh: the save, not the worktree, is
// what changed.
func TestShipShowsADraftSavedAfterTheCode(t *testing.T) {
	s, lw, _, se, _ := shipScene(t)
	x := shipOf(s)
	agent := lw.Fake.WaitStarted(t)
	s.Run("ship.open", nil)
	writeFiles(t, shipDir(se, "search-api"), map[string]string{"internal/api/usage/routes.go": "package usage\n\nvar Routes = 2\n"})
	shipSettle(s, t, "the change", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return len(sh.Files) > 0 })
	shipIdle(s)

	agent.Call(t, "commit_draft", map[string]any{"project": "search-api", "message": "feat(api): two routes"})
	shipSettle(s, t, "the draft", func() bool { return s.Node("Commit message").Value == "feat(api): two routes" })
	s.WantFrame("DRAFTED BY FAKE")
}

// A project's Git panel, in a directory opened directly: its branch, the commit box, rename and push, with no
// agent to draft or redraft.
func TestGitPanelInAnOpenedDirectory(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "You")
	t.Setenv("GIT_AUTHOR_EMAIL", "you@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "You")
	t.Setenv("GIT_COMMITTER_EMAIL", "you@example.com")
	hub := ghtest.New(t)
	lw := rig.NewHostWith(t, host.Options{GitHub: func(string, []string) gh.Client { return hub.Client() }})
	blog, up := shipRepo(t, "blog", map[string]string{"post.md": "# One\n"})
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	s.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.Run("directory.open", registry.Args{"path": blog})
	st := s.UntilState("the directory's session in front", func(st core.State) bool {
		se, ok := st.Session(st.Focus)
		return ok && se.Snapshot().Kind == core.KindDirect
	})
	id := st.Focus
	s.Until("the window on the directory", func() bool { s.RunQueued(); return s.Reopen() })
	x := shipOf(s)

	s.Run("project.panel", registry.Args{"name": "git"})
	shipSettle(s, t, "the Git panel", func() bool { return strings.Contains(s.Text(), "Nothing uncommitted.") })
	s.WantFrame("main", "→ main", "Rebase on main", "Push", "Rename…")

	writeFiles(t, blog, map[string]string{"post.md": "# Two\n"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the change", func() bool { return strings.Contains(s.Text(), "CHANGES · 1") })
	if strings.Contains(s.Text(), "Redraft") {
		t.Error("Redraft shows with no agent to ask")
	}
	s.TypeInto("Commit message", "docs: two")
	s.ClickOn("Commit")
	shipSettle(s, t, "the commit", func() bool { sh, _ := x.ShipOf(id, "blog"); return len(sh.Files) == 0 && len(sh.Unpushed) == 1 })
	s.WantFrame("TO PUSH · 1", "docs: two")

	s.ClickOn("Rename…")
	s.Frame()
	s.TypeInto("Branch name", "two")
	s.ClickOn("Rename")
	shipSettle(s, t, "the rename", func() bool { sh, _ := x.ShipOf(id, "blog"); return sh.Branch == "two" })
	if got := strings.TrimSpace(git(t, blog, "rev-parse", "--abbrev-ref", "HEAD")); got != "two" {
		t.Fatalf("the checkout is on %q", got)
	}

	s.ClickOn("Push")
	shipSettle(s, t, "the push", func() bool { sh, _ := x.ShipOf(id, "blog"); return sh.Pushed && len(sh.Unpushed) == 0 })
	if out := git(t, up, "ls-remote", "origin", "two"); !strings.Contains(out, "refs/heads/two") {
		t.Errorf("origin hasn't got two: %q", out)
	}
}

// With its PR open, the header offers Push for new commits, not another raise.
func TestShipHeaderPushesToAnOpenPR(t *testing.T) {
	s, _, hub, se, _ := shipScene(t)
	x := shipOf(s)
	dir := shipDir(se, "search-api")
	writeFiles(t, dir, map[string]string{"x.go": "package x\n"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the file", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return len(sh.Files) == 1 })
	s.Run("ship.commit", registry.Args{"project": "search-api", "message": "feat: x"})
	shipSettle(s, t, "the commit", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.Commits == 1 })
	s.Run("ship.raise", registry.Args{"project": "search-api", "title": "feat: x", "body": "x"})
	shipSettle(s, t, "the PR", func() bool {
		sh, _ := x.ShipOf(se.ID, "search-api")
		return sh.PR != nil && x.Doing(se.ID, "search-api") == ""
	})
	s.Run("ship.open", nil)
	s.Frame()
	if s.Find("Push + raise PR") != nil || s.Find("Push all") != nil {
		t.Fatalf("the header offers a raise or a push with the PR open and nothing to push:\n%s", s.Text())
	}

	writeFiles(t, dir, map[string]string{"y.go": "package x\n"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the file", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return len(sh.Files) == 1 })
	s.Run("ship.commit", registry.Args{"project": "search-api", "message": "feat: y"})
	shipSettle(s, t, "the unpushed commit", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return len(sh.Unpushed) == 1 })
	s.Run("ship.open", nil)
	s.Frame()
	if s.Find("Push + raise PR") != nil {
		t.Fatal("the header offers a raise with the PR open")
	}
	s.ClickOn("Push all")
	shipSettle(s, t, "pushed", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return len(sh.Unpushed) == 0 && sh.Pushed })
	if n := len(hub.PRs("acme/search-api")); n != 1 {
		t.Errorf("Push all raised another PR: %d", n)
	}
}
