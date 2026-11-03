package ship_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// Closing the tab of a session whose PRs are all merged, with nothing uncommitted or unpushed, offers to end it,
// its local branches ticked to go; ⌘↵ archives it and deletes them. While its PR is open, the tab just closes.
func TestClosingAShippedSessionOffersToEndIt(t *testing.T) {
	s, _, hub, se, pr := raisedScene(t)
	x := shipOf(s)
	closeTab := func() {
		t.Helper()
		s.Until("its tab", func() bool { return s.FindRole(render.RoleTab, se.Name) != nil })
		s.RightClick(apptest.Centre(s.NodeRole(render.RoleTab, se.Name).Bounds))
		s.ClickText("Close tab")
		s.UntilState("the tab closed", func(st core.State) bool { return len(st.Tabs) == 0 })
	}

	closeTab()
	s.Frames(3)
	if s.Has("END SESSION") {
		t.Fatal("closing a session with an open PR offered to end it")
	}
	s.Run("session.focus", registry.Args{"id": string(se.ID)})

	hub.Merge("acme/search-api", pr.Number)
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the PR merged", func() bool { sh, _ := x.ShipOf(se.ID, "search-api"); return sh.PR != nil && sh.PR.Merged })
	branch, _ := x.ShipOf(se.ID, "search-api")
	closeTab()
	s.Until("the End session dialog", func() bool { return s.Has("END SESSION", "Every pull request is merged") })
	if b := s.Node("Delete the local branches too (remote branches stay)"); b.Checked == nil || !*b.Checked {
		t.Error("deleting the local branches isn't ticked")
	}
	s.WantFrame("Archive")
	s.Press("<D-CR>")
	s.UntilState("the session archived", func(st core.State) bool {
		sess, ok := st.Session(se.ID)
		return ok && sess.Snapshot().Archived
	})
	repo := ""
	for _, p := range se.Projects {
		if p.Name == "search-api" {
			repo = p.Repo
		}
	}
	s.Until("the branch gone", func() bool { return !strings.Contains(git(t, repo, "branch", "--list"), branch.Branch) })
}
