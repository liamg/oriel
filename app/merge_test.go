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
	x := s.ship()
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
	s.shipSettle(t, "merged", func() bool { sh, _ := x.ship(se.ID, "search-api"); return sh.PR != nil && sh.PR.Merged })
	if got := hub.MergedWith("acme/search-api", pr.Number); got != registry.MergeSquash {
		t.Errorf("merged with %q, want squash", got)
	}
}

// hosts.md: "A hard one (the host refuses: conflicts, required reviews) shows Continue off, with why." The method
// follows ship.merge_method when the repo allows it.
func TestShipMergeHardBlockerAndMethod(t *testing.T) {
	s, _, hub, se, pr := raisedScene(t)
	x := s.ship()
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
		return strings.Contains(f, "MERGE #") && !strings.Contains(f, "Reading what stands")
	})
	if strings.Contains(s.frameText(), "won't merge it") {
		t.Fatal("a clean PR still refused")
	}
	if n := s.access("rebase"); !n.Selected {
		t.Errorf("ship.merge_method rebase isn't the method picked: %+v", n)
	}
	s.run("ship.merge.confirm", nil)
	s.shipSettle(t, "merged", func() bool { sh, _ := x.ship(se.ID, "search-api"); return sh.PR != nil && sh.PR.Merged })
	if got := hub.MergedWith("acme/search-api", pr.Number); got != registry.MergeRebase {
		t.Errorf("merged with %q, want rebase", got)
	}
}
