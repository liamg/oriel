package ship_test

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
)

// raisedScene is shipScene with search-api's PR raised from one commit.
func raisedScene(t *testing.T) (*apptest.Scene, *rig.Host, *ghtest.Server, core.SessionState, gh.PR) {
	t.Helper()
	s, lw, hub, se, _ := shipScene(t)
	x := shipOf(s)
	writeFiles(t, shipDir(se, "search-api"), map[string]string{"x.go": "package x\n"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the file", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return len(sh.Files) == 1 })
	s.Run("ship.commit", registry.Args{"project": "search-api", "message": "feat: x"})
	shipSettle(s, t, "the commit", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.Commits == 1 })
	s.Run("ship.raise", registry.Args{"project": "search-api", "title": "feat: x"})
	shipSettle(s, t, "the PR", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.PR != nil })
	return s, lw, hub, se, hub.PRs("acme/search-api")[0]
}

// The merge guard: before merging, the app asks the provider's Blockers; if there are some, a dialog lists them, with
// Continue (merge anyway) or Cancel. Unresolved counts both kinds, and says which: "3 reviewer comments, 2 Oriel
// threads unresolved". The merge method is ship.merge_method, unset meaning the repo's default, else squash.
func TestShipMergeWarnsAboutUnresolvedThreads(t *testing.T) {
	s, lw, hub, se, pr := raisedScene(t)
	root := hub.AddReviewComment("acme/search-api", pr.Number, gh.ReviewComment{User: "priya-k", Path: "x.go", Line: 1, Body: "why?"})
	hub.AddReviewComment("acme/search-api", pr.Number, gh.ReviewComment{User: "priya-k", Path: "x.go", Line: 1, Body: "and this"})
	hub.Resolve(root.ID, false)
	ctx := context.Background()
	for _, body := range []string{"rename this", "and test it"} {
		if _, err := lw.W.Comment(ctx, host.CommentReq{Session: se.ID, Body: body}); err != nil {
			t.Fatal(err)
		}
	}

	open := 0
	st := s.Store().State()
	sess, _ := st.Session(se.ID)
	for _, th := range sess.Snapshot().Threads {
		if th.Open() {
			open++
		}
	}
	if open < 2 {
		t.Fatalf("%d open threads on the session", open)
	}

	s.Run("ship.merge", registry.Args{"project": "search-api"})
	s.Until("the warning", func() bool { shipIdle(s); return strings.Contains(s.Text(), "unresolved") })
	s.Lint("the merge dialog")
	s.WantFrame("MERGE #", fmt.Sprintf("2 reviewer comments, %d Oriel threads unresolved", open), "squash", "Continue", "Cancel")
	s.ClickText("Cancel")
	s.Until("the dialog gone", func() bool { return !strings.Contains(s.Text(), "unresolved") })
	if hub.MergedWith("acme/search-api", pr.Number) != "" {
		t.Fatal("Cancel merged it")
	}

	s.Run("ship.merge", registry.Args{"project": "search-api"})
	s.Until("the warning again", func() bool { shipIdle(s); return strings.Contains(s.Text(), "unresolved") })
	s.ClickText("Continue")
	shipSettle(s, t, "merged", func() bool { return hub.MergedWith("acme/search-api", pr.Number) != "" })
	if got := hub.MergedWith("acme/search-api", pr.Number); got != registry.MergeSquash {
		t.Errorf("merged with %q, want squash", got)
	}
}

// A hard blocker (the host refuses: conflicts, required reviews) shows Continue off, with why. The method
// follows ship.merge_method when the repo allows it.
func TestShipMergeHardBlockerAndMethod(t *testing.T) {
	s, _, hub, _, pr := raisedScene(t)
	if err := s.Registry().Options().Set("ship.merge_method", registry.LayerUser, registry.At{}, "rebase"); err != nil {
		t.Fatal(err)
	}
	hub.SetMergeable("acme/search-api", pr.Number, "CONFLICTING", "DIRTY")
	s.Run("ship.merge", registry.Args{"project": "search-api"})
	s.Until("the warning", func() bool { shipIdle(s); return strings.Contains(s.Text(), "conflicts with main") })
	if n := s.Node("Continue ↵"); !n.Disabled {
		t.Fatalf("Continue past a conflict: %+v", n)
	}
	s.WantFrame("GitHub won't merge it")
	s.Run("ship.merge.cancel", nil)
	s.Until("the dialog gone", func() bool { return !strings.Contains(s.Text(), "MERGE #") })

	hub.SetMergeable("acme/search-api", pr.Number, "MERGEABLE", "CLEAN")
	s.Run("ship.merge", registry.Args{"project": "search-api"})
	s.Until("the confirm", func() bool {
		shipIdle(s)
		f := s.Text()
		return strings.Contains(f, "MERGE #") && strings.Contains(f, "Method")
	})
	if strings.Contains(s.Text(), "won't merge it") {
		t.Fatal("a clean PR still refused")
	}
	if n := s.Node("rebase"); !n.Selected {
		t.Errorf("ship.merge_method rebase isn't the method picked: %+v", n)
	}
	s.Run("ship.merge.confirm", nil)
	shipSettle(s, t, "merged", func() bool { return hub.MergedWith("acme/search-api", pr.Number) != "" })
	if got := hub.MergedWith("acme/search-api", pr.Number); got != registry.MergeRebase {
		t.Errorf("merged with %q, want rebase", got)
	}
}

// pastPRs are a session's PRs it has moved on from.
func pastPRs(s *apptest.Scene, id core.SessionID) []core.PastPR {
	sess, _ := s.Store().State().Session(id)
	return sess.Snapshot().Past
}

// Start next PR cuts a new branch from the updated base inside the same session; earlier PRs stay readable under
// Previous. Merged from Oriel with nothing in the way, the next PR starts by itself.
func TestMergeStartsTheNextPR(t *testing.T) {
	s, _, hub, se, pr := raisedScene(t)
	x := shipOf(s)
	first, _ := x.ShipOf(se.ID, "search-api")
	hub.SetMergeable("acme/search-api", pr.Number, "MERGEABLE", "CLEAN")
	s.Run("ship.merge", registry.Args{"project": "search-api"})
	s.Until("the confirm", func() bool { shipIdle(s); return strings.Contains(s.Text(), "Method") })
	s.Run("ship.merge.confirm", nil)
	shipSettle(s, t, "the next PR", func() bool { return len(pastPRs(s, se.ID)) == 1 })
	past := pastPRs(s, se.ID)[0]
	if past.Project != "search-api" || past.Number != pr.Number || past.State != "merged" || past.Branch != first.Branch || past.URL != pr.URL {
		t.Errorf("past = %+v", past)
	}
	shipSettle(s, t, "the fresh branch", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.Branch != first.Branch && sh.PR == nil })

	if !x.Fired(pr.URL) {
		t.Error("PrMerged didn't fire for the PR merged here")
	}

	s.Run("ship.pr.open", registry.Args{"project": "search-api"})
	s.Until("the Raise PR tab", func() bool { return strings.Contains(s.Text(), "RAISE PR") })
	s.WantFrame("PREVIOUS", fmt.Sprintf("#%d · feat: x", pr.Number), "merged")

	// Moved on from a merged PR with nothing since, the project still counts as merged: Ship offers Finish session.
	s.Run("ship.open", nil)
	s.Until("Finish session", func() bool { return strings.Contains(s.Text(), "Finish session") })
}

// With session.archive_on_merge on, a merge that leaves every PR merged archives the session; no next PR starts.
func TestMergeArchivesRatherThanStartingTheNextPR(t *testing.T) {
	s, _, hub, se, pr := raisedScene(t)
	if err := s.Registry().Options().Set(kit.ArchiveOnMergeOption, registry.LayerUser, registry.At{}, true); err != nil {
		t.Fatal(err)
	}
	hub.SetMergeable("acme/search-api", pr.Number, "MERGEABLE", "CLEAN")
	s.Run("ship.merge", registry.Args{"project": "search-api"})
	s.Until("the confirm", func() bool { shipIdle(s); return strings.Contains(s.Text(), "Method") })
	s.Run("ship.merge.confirm", nil)
	s.UntilState("the session archived", func(st core.State) bool {
		shipIdle(s)
		sess, ok := st.Session(se.ID)
		return ok && sess.Snapshot().Archived
	})
	shipIdle(s)
	if p := pastPRs(s, se.ID); len(p) != 0 {
		t.Errorf("the next PR started too: %+v", p)
	}
}

// Merged on GitHub, the next PR is asked about, not started: the agent or your edits may be mid-way on the branch.
func TestMergedOnGitHubAsksToStartTheNextPR(t *testing.T) {
	s, _, hub, se, pr := raisedScene(t)
	x := shipOf(s)
	first, _ := x.ShipOf(se.ID, "search-api")
	hub.Merge("acme/search-api", pr.Number)
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the question", func() bool { return strings.Contains(s.Text(), "Start the next PR in search-api") })
	if len(pastPRs(s, se.ID)) != 0 {
		t.Fatal("the next PR started without asking")
	}
	s.WantFrame(fmt.Sprintf("#%d MERGED", pr.Number), "Not now")
	s.ClickText("Start next PR")
	shipSettle(s, t, "the next PR", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.Branch != first.Branch && sh.PR == nil })
	if p := pastPRs(s, se.ID); len(p) != 1 || p[0].State != "merged" {
		t.Errorf("past = %+v", p)
	}
}

// Merged into main here, the next PR starts after the merge; until main is pushed, raising it would show the merged
// commits again, so it's refused and the panel says main isn't pushed.
func TestNextPRAfterMergingIntoMainHereWaitsForMainToBePushed(t *testing.T) {
	s, _, _, se, _ := raisedScene(t)
	x := shipOf(s)
	first, _ := x.ShipOf(se.ID, "search-api")
	s.Run("ship.merge-default", registry.Args{"project": "search-api", "confirm": true})
	shipSettle(s, t, "the next PR", func() bool {
		sh, _ := x.ShipOf(se.ID, "search-api")
		return sh.Branch != first.Branch && sh.Branch != ""
	})
	if p := pastPRs(s, se.ID); len(p) != 1 || p[0].Branch != first.Branch {
		t.Fatalf("past = %+v", p)
	}
	dir := shipDir(se, "search-api")
	writeFiles(t, dir, map[string]string{"y.go": "package x\n"})
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "feat: y")
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "main unpushed", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.BaseAhead == 1 })
	s.Run("project.open", registry.Args{"project": "search-api"})
	s.Run("project.panel", registry.Args{"name": "pull-request"})
	s.Frame()
	s.WantFrame("main pushed", "1 to push")
	err := s.Registry().Run(context.Background(), "ship.raise", registry.Args{"project": "search-api", "title": "feat: y"})
	if err == nil || !strings.Contains(err.Error(), "push main first") {
		t.Errorf("raised with main unpushed: %v", err)
	}
	if sh, _ := x.ShipOf(se.ID, "search-api"); strings.Contains(git(t, dir, "ls-remote", "origin"), "refs/heads/"+sh.Branch+"\n") {
		t.Error("the refused raise pushed the branch")
	}
}

// Mission control: a session's card names the PR it raised, a chip linking to it, beside its kind's chips.
func TestRaisedPRIsACardChip(t *testing.T) {
	s, _, _, se, pr := raisedScene(t)
	want := registry.Chip{Text: "#" + strconv.Itoa(pr.Number), Help: kit.TabsHelp, Link: pr.URL}
	if chips := registry.Chips(s.Registry(), se); !slices.Contains(chips, want) {
		t.Errorf("the card's chips %+v lack %+v", chips, want)
	}
	other := core.SessionState{ID: "nope", Kind: core.KindWork}
	if chips := registry.Chips(s.Registry(), other); len(chips) != 0 {
		t.Errorf("a session that raised nothing has chips %+v", chips)
	}
}

// Starting the next PR reads that project again, not the whole session: only its branch moved on, and the drafts it
// drops are its own.
func TestStartingTheNextPRReadsOnlyItsProject(t *testing.T) {
	s, _, hub, se, pr := raisedScene(t)
	x := shipOf(s)
	first, _ := x.ShipOf(se.ID, "search-api")
	hub.Merge("acme/search-api", pr.Number)
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the question", func() bool { return strings.Contains(s.Text(), "Start the next PR in search-api") })
	shipQuiet(s)
	whole := x.WholeReads(se.ID)
	s.ClickText("Start next PR")
	shipSettle(s, t, "the next PR", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.Branch != first.Branch && sh.PR == nil })
	shipQuiet(s)
	if n := x.WholeReads(se.ID) - whole; n > 0 {
		t.Errorf("starting search-api's next PR read every project %d times", n)
	}
}

// Switching back to a session synced a moment ago doesn't ask the code host for every PR's comments again: its
// sync stands until it's due.
func TestSwitchingBackDoesntSyncAgainAtOnce(t *testing.T) {
	s, _, hub, se, _ := raisedScene(t)
	comments := func() int {
		n := 0
		for _, c := range hub.Calls() {
			if strings.Contains(c, "/comments") {
				n++
			}
		}
		return n
	}
	s.Apply(core.SessionFocused{ID: se.ID})
	shipQuiet(s)
	before := comments()
	other := core.SessionState{ID: "other", Name: "other", Kind: core.KindWork}
	s.Apply(core.SessionSynced{Session: other})
	for range 3 {
		s.Apply(core.SessionFocused{ID: other.ID}, core.SessionFocused{ID: se.ID})
		shipQuiet(s)
	}
	if n := comments() - before; n > 0 {
		t.Errorf("switching back 3 times asked for the PR's comments %d times", n)
	}
}
