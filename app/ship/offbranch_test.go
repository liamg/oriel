package ship_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// branchOfProject is the session's branch for a project, as the store has it.
func branchOfProject(s *apptest.Scene, id core.SessionID, project string) string {
	se, ok := s.Store().State().Session(id)
	if !ok {
		return ""
	}
	for _, p := range se.Snapshot().Projects {
		if p.Name == project {
			return p.Branch
		}
	}
	return ""
}

// checkout runs git checkout in a worktree as the agent would, and tells the app's file watchers its HEAD moved.
func checkout(t *testing.T, s *apptest.Scene, dir string, args ...string) {
	t.Helper()
	git(t, dir, append([]string{"checkout", "-q"}, args...)...)
	s.FilesChanged(filepath.Join(strings.TrimSpace(git(t, dir, "rev-parse", "--absolute-git-dir")), "HEAD"))
}

// help git/ship#branch: "When a project's worktree is on a branch other than the session's (the agent ran
// git checkout -b, say), Oriel won't push it, and the Git panel and the Ship page say so, with Switch back to <branch> and
// Make <branch> this session's branch."
func TestGitPanelAdoptsTheAgentsBranch(t *testing.T) {
	s, _, _, se, _ := shipScene(t)
	dir := shipDir(se, "search-api")
	s.Run("project.panel", registry.Args{"name": "git", "project": "search-api"})
	shipSettle(s, t, "the panel", func() bool { return strings.Contains(s.Text(), "→ main") })
	checkout(t, s, dir, "-b", "agent/own")
	writeFiles(t, dir, map[string]string{"internal/api/usage/handler.go": "package usage\n\nfunc Handle() { _ = 1 }\n"})
	git(t, dir, "commit", "-qam", "feat: one")

	adopt := "Make agent/own this session's branch"
	shipSettle(s, t, "the warning", func() bool { return strings.Contains(s.Text(), "Not on this session's branch") && clickable(s, adopt) })
	if clickable(s, "Push") {
		t.Errorf("Push is offered on a branch Oriel won't push:\n%s", s.Text())
	}
	s.ClickOn(adopt)
	shipSettle(s, t, "the branch adopted", func() bool {
		return branchOfProject(s, se.ID, "search-api") == "agent/own" && !strings.Contains(s.Text(), "Not on this session's branch")
	})
	shipSettle(s, t, "Push", func() bool { return clickable(s, "Push") })
}

// Switch back puts the worktree on the session's branch again.
func TestGitPanelSwitchesBackToTheSessionsBranch(t *testing.T) {
	s, _, _, se, _ := shipScene(t)
	dir := shipDir(se, "search-api")
	own := branchOfProject(s, se.ID, "search-api")
	s.Run("project.panel", registry.Args{"name": "git", "project": "search-api"})
	shipSettle(s, t, "the panel", func() bool { return strings.Contains(s.Text(), "→ main") })
	checkout(t, s, dir, "-b", "agent/own")

	back := "Switch back to " + own
	shipSettle(s, t, "the warning", func() bool { return clickable(s, back) })
	s.ClickOn(back)
	shipSettle(s, t, "the worktree back", func() bool {
		return strings.TrimSpace(git(t, dir, "rev-parse", "--abbrev-ref", "HEAD")) == own && !strings.Contains(s.Text(), "Not on this session's branch")
	})
	if got := branchOfProject(s, se.ID, "search-api"); got != own {
		t.Errorf("the session's branch moved to %q", got)
	}
}

// The Ship page shows it too, with the project's column even when it has nothing to ship; what can't be adopted is
// off, saying why.
func TestShipPageWontAdoptADetachedHead(t *testing.T) {
	s, _, _, se, _ := shipScene(t)
	dir := shipDir(se, "web-dashboard")
	git(t, dir, "checkout", "-q", "--detach")
	s.Run("ship.open", nil)
	adopt := "Make it this session's branch"
	shipSettle(s, t, "the warning", func() bool {
		return strings.Contains(s.Text(), "Not on this session's branch") && s.Find(adopt) != nil
	})
	if n := s.Node(adopt); !n.Disabled {
		t.Error("a detached HEAD can be adopted")
	}
	s.WantFrame("detached")
	s.Run("ship.adopt-branch", registry.Args{"project": "web-dashboard"})
	shipSettle(s, t, "the refusal", func() bool { return strings.Contains(s.Text(), "can't take its branch") })
	if strings.TrimSpace(git(t, dir, "rev-parse", "--abbrev-ref", "HEAD")) != "HEAD" {
		t.Error("the worktree moved")
	}
}
