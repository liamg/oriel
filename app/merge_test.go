package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
)

// raisedScene is shipScene with search-api's PR raised from one commit.
func raisedScene(t *testing.T) (*errScene, *localHost, *ghtest.Server, core.SessionState, gh.PR) {
	t.Helper()
	s, lw, hub, se, _ := shipScene(t)
	x := s.ship()
	writeFiles(t, shipDir(se, "search-api"), map[string]string{"x.go": "package x\n"})
	s.run("ship.refresh", nil)
	s.shipSettle(t, "the file", func() bool { sh, _ := x.ship(se.ID, "search-api"); return len(sh.Files) == 1 })
	s.run("ship.commit", registry.Args{"project": "search-api", "message": "feat: x"})
	s.shipSettle(t, "the commit", func() bool { sh, _ := x.ship(se.ID, "search-api"); return sh.Commits == 1 })
	s.run("ship.raise", registry.Args{"project": "search-api", "title": "feat: x"})
	s.shipSettle(t, "the PR", func() bool { sh, _ := x.ship(se.ID, "search-api"); return sh.PR != nil })
	return s, lw, hub, se, hub.PRs("acme/search-api")[0]
}

// hosts.md, "The merge guard": "Before merging, the app asks the provider's Blockers… Some: a dialog listing them,
// Continue (merge anyway) or Cancel." "Unresolved counts both kinds, and says which: 3 reviewer comments, 2 Oriel
// threads unresolved." "Merge method: ship.merge_method, unset meaning the repo's default, else squash."
func TestShipMergeWarnsAboutUnresolvedThreads(t *testing.T) {
	s, lw, hub, se, pr := raisedScene(t)
	root := hub.AddReviewComment("acme/search-api", pr.Number, gh.ReviewComment{User: "priya-k", Path: "x.go", Line: 1, Body: "why?"})
	hub.AddReviewComment("acme/search-api", pr.Number, gh.ReviewComment{User: "priya-k", Path: "x.go", Line: 1, Body: "and this"})
	hub.Resolve(root.ID, false)
	ctx := context.Background()
	for _, body := range []string{"rename this", "and test it"} {
		if _, err := lw.w.Comment(ctx, host.CommentReq{Session: se.ID, Body: body}); err != nil {
			t.Fatal(err)
		}
	}

	open := 0
	st := s.store.State()
	sess, _ := st.Session(se.ID)
	for _, th := range sess.Snapshot().Threads {
		if th.Open() {
			open++
		}
	}
	if open < 2 {
		t.Fatalf("%d open threads on the session", open)
	}

	s.run("ship.merge", registry.Args{"project": "search-api"})
	s.until(t, "the warning", func() bool { s.shipIdle(); return strings.Contains(s.frameText(), "unresolved") })
	s.lint("the merge dialog")
	s.wantFrame(t, "MERGE #", fmt.Sprintf("2 reviewer comments, %d Oriel threads unresolved", open), "squash", "Continue", "Cancel")
	s.clickText(t, "Cancel")
	s.until(t, "the dialog gone", func() bool { return !strings.Contains(s.frameText(), "unresolved") })
	if hub.MergedWith("acme/search-api", pr.Number) != "" {
		t.Fatal("Cancel merged it")
	}

	s.run("ship.merge", registry.Args{"project": "search-api"})
	s.until(t, "the warning again", func() bool { s.shipIdle(); return strings.Contains(s.frameText(), "unresolved") })
	s.clickText(t, "Continue")
	s.shipSettle(t, "merged", func() bool { return hub.MergedWith("acme/search-api", pr.Number) != "" })
	if got := hub.MergedWith("acme/search-api", pr.Number); got != registry.MergeSquash {
		t.Errorf("merged with %q, want squash", got)
	}
}

// hosts.md: "A hard one (the host refuses: conflicts, required reviews) shows Continue off, with why." The method
// follows ship.merge_method when the repo allows it.
func TestShipMergeHardBlockerAndMethod(t *testing.T) {
	s, _, hub, _, pr := raisedScene(t)
	if err := s.reg.Options().Set("ship.merge_method", registry.LayerUser, registry.At{}, "rebase"); err != nil {
		t.Fatal(err)
	}
	hub.SetMergeable("acme/search-api", pr.Number, "CONFLICTING", "DIRTY")
	s.run("ship.merge", registry.Args{"project": "search-api"})
	s.until(t, "the warning", func() bool { s.shipIdle(); return strings.Contains(s.frameText(), "conflicts with main") })
	if n := s.access("Continue ↵"); !n.Disabled {
		t.Fatalf("Continue past a conflict: %+v", n)
	}
	s.wantFrame(t, "GitHub won't merge it")
	s.run("ship.merge.cancel", nil)
	s.until(t, "the dialog gone", func() bool { return !strings.Contains(s.frameText(), "MERGE #") })

	hub.SetMergeable("acme/search-api", pr.Number, "MERGEABLE", "CLEAN")
	s.run("ship.merge", registry.Args{"project": "search-api"})
	s.until(t, "the confirm", func() bool {
		s.shipIdle()
		f := s.frameText()
		return strings.Contains(f, "MERGE #") && strings.Contains(f, "Method")
	})
	if strings.Contains(s.frameText(), "won't merge it") {
		t.Fatal("a clean PR still refused")
	}
	if n := s.access("rebase"); !n.Selected {
		t.Errorf("ship.merge_method rebase isn't the method picked: %+v", n)
	}
	s.run("ship.merge.confirm", nil)
	s.shipSettle(t, "merged", func() bool { return hub.MergedWith("acme/search-api", pr.Number) != "" })
	if got := hub.MergedWith("acme/search-api", pr.Number); got != registry.MergeRebase {
		t.Errorf("merged with %q, want rebase", got)
	}
}

// pastPRs are a session's PRs it has moved on from.
func pastPRs(s *errScene, id core.SessionID) []core.PastPR {
	sess, _ := s.store.State().Session(id)
	return sess.Snapshot().Past
}

// sessions.md: "Start next PR cuts a new branch from the updated base inside the same session. Earlier PRs stay
// readable under Previous." Merged from Oriel with nothing in the way, the next PR starts by itself.
func TestMergeStartsTheNextPR(t *testing.T) {
	s, _, hub, se, pr := raisedScene(t)
	x := s.ship()
	first, _ := x.ship(se.ID, "search-api")
	hub.SetMergeable("acme/search-api", pr.Number, "MERGEABLE", "CLEAN")
	s.run("ship.merge", registry.Args{"project": "search-api"})
	s.until(t, "the confirm", func() bool { s.shipIdle(); return strings.Contains(s.frameText(), "Method") })
	s.run("ship.merge.confirm", nil)
	s.shipSettle(t, "the next PR", func() bool { return len(pastPRs(s, se.ID)) == 1 })
	past := pastPRs(s, se.ID)[0]
	if past.Project != "search-api" || past.Number != pr.Number || past.State != "merged" || past.Branch != first.Branch || past.URL != pr.URL {
		t.Errorf("past = %+v", past)
	}
	s.shipSettle(t, "the fresh branch", func() bool { sh, _ := x.ship(se.ID, "search-api"); return sh.Branch != first.Branch && sh.PR == nil })

	x.mu.Lock()
	fired := x.merged[pr.URL]
	x.mu.Unlock()
	if !fired {
		t.Error("PrMerged didn't fire for the PR merged here")
	}

	s.run("ship.pr.open", registry.Args{"project": "search-api"})
	s.until(t, "the Raise PR tab", func() bool { return strings.Contains(s.frameText(), "RAISE PR") })
	s.wantFrame(t, "PREVIOUS", fmt.Sprintf("#%d · feat: x", pr.Number), "merged")

	// Moved on from a merged PR with nothing since, the project still counts as merged: Ship offers Finish session.
	s.run("ship.open", nil)
	s.until(t, "Finish session", func() bool { return strings.Contains(s.frameText(), "Finish session") })
}

// With session.archive_on_merge on, a merge that leaves every PR merged archives the session; no next PR starts.
func TestMergeArchivesRatherThanStartingTheNextPR(t *testing.T) {
	s, _, hub, se, pr := raisedScene(t)
	if err := s.reg.Options().Set(archiveOnMerge, registry.LayerUser, registry.At{}, true); err != nil {
		t.Fatal(err)
	}
	hub.SetMergeable("acme/search-api", pr.Number, "MERGEABLE", "CLEAN")
	s.run("ship.merge", registry.Args{"project": "search-api"})
	s.until(t, "the confirm", func() bool { s.shipIdle(); return strings.Contains(s.frameText(), "Method") })
	s.run("ship.merge.confirm", nil)
	s.untilState(t, "the session archived", func(st core.State) bool {
		s.shipIdle()
		sess, ok := st.Session(se.ID)
		return ok && sess.Snapshot().Archived
	})
	s.shipIdle()
	if p := pastPRs(s, se.ID); len(p) != 0 {
		t.Errorf("the next PR started too: %+v", p)
	}
}

// Merged on GitHub, the next PR is asked about, not started: the agent or your edits may be mid-way on the branch.
func TestMergedOnGitHubAsksToStartTheNextPR(t *testing.T) {
	s, _, hub, se, pr := raisedScene(t)
	x := s.ship()
	first, _ := x.ship(se.ID, "search-api")
	hub.Merge("acme/search-api", pr.Number)
	s.run("ship.refresh", nil)
	s.shipSettle(t, "the question", func() bool { return strings.Contains(s.frameText(), "Start the next PR in search-api") })
	if len(pastPRs(s, se.ID)) != 0 {
		t.Fatal("the next PR started without asking")
	}
	s.wantFrame(t, fmt.Sprintf("#%d MERGED", pr.Number), "Not now")
	s.clickText(t, "Start next PR")
	s.shipSettle(t, "the next PR", func() bool { sh, _ := x.ship(se.ID, "search-api"); return sh.Branch != first.Branch && sh.PR == nil })
	if p := pastPRs(s, se.ID); len(p) != 1 || p[0].State != "merged" {
		t.Errorf("past = %+v", p)
	}
}

// Merged into main here, the next PR starts after the merge; until main is pushed, raising it would show the merged
// commits again, so it's refused and the panel says main isn't pushed.
func TestNextPRAfterMergingIntoMainHereWaitsForMainToBePushed(t *testing.T) {
	s, _, _, se, _ := raisedScene(t)
	x := s.ship()
	first, _ := x.ship(se.ID, "search-api")
	s.run("ship.merge-default", registry.Args{"project": "search-api", "confirm": true})
	s.shipSettle(t, "the next PR", func() bool { sh, _ := x.ship(se.ID, "search-api"); return sh.Branch != first.Branch && sh.Branch != "" })
	if p := pastPRs(s, se.ID); len(p) != 1 || p[0].Branch != first.Branch {
		t.Fatalf("past = %+v", p)
	}
	dir := shipDir(se, "search-api")
	writeFiles(t, dir, map[string]string{"y.go": "package x\n"})
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "feat: y")
	s.run("ship.refresh", nil)
	s.shipSettle(t, "main unpushed", func() bool { sh, _ := x.ship(se.ID, "search-api"); return sh.BaseAhead == 1 })
	s.run("project.open", registry.Args{"project": "search-api"})
	s.run("project.panel", registry.Args{"name": "pull-request"})
	s.frame()
	s.wantFrame(t, "main pushed", "1 to push")
	err := s.reg.Run(context.Background(), "ship.raise", registry.Args{"project": "search-api", "title": "feat: y"})
	if err == nil || !strings.Contains(err.Error(), "push main first") {
		t.Errorf("raised with main unpushed: %v", err)
	}
	if sh, _ := x.ship(se.ID, "search-api"); strings.Contains(git(t, dir, "ls-remote", "origin"), "refs/heads/"+sh.Branch+"\n") {
		t.Error("the refused raise pushed the branch")
	}
}
