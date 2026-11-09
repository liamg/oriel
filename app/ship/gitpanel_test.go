package ship_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/ship"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// gitPanelScene is a directory session on blog, a checkout with origin on GitHub (a fake), its Git panel open and
// read. prepare runs on the checkout before it's opened.
func gitPanelScene(t *testing.T, prepare func(dir string)) (s *apptest.Scene, x *ship.Ship, id core.SessionID, blog, upstream string) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "You")
	t.Setenv("GIT_AUTHOR_EMAIL", "you@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "You")
	t.Setenv("GIT_COMMITTER_EMAIL", "you@example.com")
	hub := ghtest.New(t)
	lw := rig.NewHostWith(t, host.Options{GitHub: func(string, []string) gh.Client { return hub.Client() }})
	blog, upstream = shipRepo(t, "blog", map[string]string{"post.md": "# One\n"})
	if prepare != nil {
		prepare(blog)
	}
	s = apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	s.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.Run("directory.open", registry.Args{"path": blog})
	st := s.UntilState("the directory's session in front", func(st core.State) bool {
		se, ok := st.Session(st.Focus)
		return ok && se.Snapshot().Kind == core.KindDirect
	})
	id = st.Focus
	s.Until("the window on the directory", func() bool { s.RunQueued(); return s.Reopen() })
	x = shipOf(s)
	s.Run("project.panel", registry.Args{"name": "git"})
	shipSettle(s, t, "the Git panel", func() bool { return strings.Contains(s.Text(), "→ main") })
	return s, x, id, blog, upstream
}

// clickable says the frame has an enabled button named label.
func clickable(s *apptest.Scene, label string) bool {
	var find func(n *render.AccessNode) bool
	find = func(n *render.AccessNode) bool {
		if n.Name == label && n.Role == render.RoleButton && !n.Disabled {
			return true
		}
		for _, k := range n.Children {
			if find(k) {
				return true
			}
		}
		return false
	}
	return find(s.UI().AccessTree())
}

// No screen looks empty, broken or finished while its data is on the way, and Ship's step actions keep their submit
// busy with the reason. From the click until the panel's read shows the commit, Commit stays
// busy and the form isn't offered again.
func TestGitPanelCommitStaysBusyUntilItShows(t *testing.T) {
	s, x, id, blog, _ := gitPanelScene(t, func(dir string) {
		writeFiles(t, dir, map[string]string{"post.md": "# Two\n"})
	})
	shipSettle(s, t, "the change", func() bool { return strings.Contains(s.Text(), "CHANGES · 1") })
	s.TypeInto("Commit message", "docs: two")
	s.ClickOn("Commit")
	busy := false
	framesUntil(s, t, "the commit", func(n int) {
		text := s.Text()
		if strings.Contains(text, "Committing…") {
			busy = true
		}
		if clickable(s, "Commit") {
			t.Fatalf("frame %d after the click shows Commit to press again:\n%s", n, text)
		}
		if n == 0 && !strings.Contains(text, "Committing…") {
			t.Fatalf("the first frame after the click doesn't say it's committing:\n%s", text)
		}
		if strings.Contains(text, "Committing…") {
			if m := s.Node("Commit message"); !m.Disabled {
				t.Fatalf("frame %d: the message can be typed into while committing", n)
			}
		}
	}, func() bool {
		text := s.Text()
		return strings.Contains(text, "Nothing uncommitted.") && strings.Contains(text, "TO PUSH · 1") && !strings.Contains(text, "Committing…")
	})
	if !busy {
		t.Error("no frame said it was committing")
	}
	if got := strings.TrimSpace(git(t, blog, "log", "-1", "--format=%s")); got != "docs: two" {
		t.Errorf("the last commit is %q", got)
	}
	if sh, _ := x.ShipOf(id, "blog"); len(sh.Files) != 0 {
		t.Errorf("still uncommitted: %v", sh.Files)
	}
}

// A commit git refuses (a pre-commit hook that fails) says why, and the form comes back as it was, the message kept.
func TestGitPanelCommitFailureKeepsTheMessage(t *testing.T) {
	s, _, _, blog, _ := gitPanelScene(t, func(dir string) {
		writeFiles(t, dir, map[string]string{"post.md": "# Two\n"})
		hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
		if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'no commits today' >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	})
	shipSettle(s, t, "the change", func() bool { return strings.Contains(s.Text(), "CHANGES · 1") })
	s.TypeInto("Commit message", "docs: two")
	s.ClickOn("Commit")
	shipSettle(s, t, "the failure", func() bool { return strings.Contains(s.Text(), "no commits today") })
	s.WantFrame("CHANGES · 1", "docs: two")
	if !clickable(s, "Commit") {
		t.Errorf("Commit isn't back:\n%s", s.Text())
	}
	if m := s.Node("Commit message"); m.Disabled {
		t.Error("the message is still off after the failure")
	}
	if got := strings.TrimSpace(git(t, blog, "log", "-1", "--format=%s")); got != "first" {
		t.Errorf("a commit was made: %q", got)
	}
}

// The Git panel's ⋯ menu merges the branch into the repo's default branch, here and not pushed, after asking with
// both branches named.
func TestGitPanelMergesIntoTheDefaultBranch(t *testing.T) {
	s, x, id, blog, _ := gitPanelScene(t, func(dir string) {
		git(t, dir, "checkout", "-q", "-b", "feature")
		writeFiles(t, dir, map[string]string{"post.md": "# Two\n"})
		git(t, dir, "commit", "-qam", "two")
	})
	shipSettle(s, t, "the branch", func() bool { sh, _ := x.ShipOf(id, "blog"); return sh.Branch == "feature" && sh.Default == "main" })
	main := strings.TrimSpace(git(t, blog, "rev-parse", "main"))

	s.ClickNode("More git actions")
	s.ClickNode("Merge into main…")
	var ask core.Notification
	for _, n := range s.Store().State().Notifications {
		if n.ID == "ship.merge-default" && !n.Dismissed {
			ask = n
		}
	}
	if ask.ID == "" || len(ask.Actions) == 0 || !strings.Contains(ask.Text, "feature") || !strings.Contains(ask.Title+ask.Text, "main") {
		t.Fatalf("the merge didn't ask first, naming both branches: %+v", ask)
	}
	if got := strings.TrimSpace(git(t, blog, "rev-parse", "main")); got != main {
		t.Fatal("main moved before the answer")
	}
	s.Run(ask.Actions[0].Command, registry.Args(ask.Actions[0].Args))
	shipSettle(s, t, "the merge", func() bool {
		return strings.TrimSpace(git(t, blog, "rev-parse", "main")) == strings.TrimSpace(git(t, blog, "rev-parse", "feature"))
	})
	if got := strings.TrimSpace(git(t, blog, "rev-parse", "--abbrev-ref", "HEAD")); got != "feature" {
		t.Errorf("the checkout moved to %q", got)
	}
	if out := git(t, blog, "ls-remote", "origin", "main"); strings.Contains(out, strings.TrimSpace(git(t, blog, "rev-parse", "main"))) {
		t.Error("the merge was pushed")
	}

	s.Run("ship.merge-default", registry.Args{"confirm": true})
	shipSettle(s, t, "the second merge's answer", func() bool { return strings.Contains(s.Text(), "nothing to merge") })
}

// On the default branch itself there's nothing to merge: the item is off.
func TestGitPanelMergeIsOffOnTheDefaultBranch(t *testing.T) {
	s, _, _, _, _ := gitPanelScene(t, nil)
	s.ClickNode("More git actions")
	if n := s.Node("Merge into main…"); !n.Disabled {
		t.Error("Merge into main… can be chosen on main")
	}
}

// Rebased after a push, the branch replaces what origin has: Push says Force push, and pushes with a lease.
func TestGitPanelForcePushesARebasedBranch(t *testing.T) {
	s, x, id, blog, _ := gitPanelScene(t, func(dir string) {
		git(t, dir, "checkout", "-q", "-b", "feature")
		writeFiles(t, dir, map[string]string{"post.md": "# Two\n"})
		git(t, dir, "commit", "-qam", "two")
		git(t, dir, "push", "-q", "-u", "origin", "feature")
		up := filepath.Join(filepath.Dir(dir), "upstream-blog")
		writeFiles(t, up, map[string]string{"README": "hi\n"})
		git(t, up, "add", "-A")
		git(t, up, "commit", "-qm", "docs")
		git(t, up, "push", "-q", "origin", "main")
		git(t, dir, "fetch", "-q", "origin")
	})
	shipSettle(s, t, "the branch behind main", func() bool { sh, _ := x.ShipOf(id, "blog"); return sh.Branch == "feature" && sh.Behind == 1 })
	s.ClickOn("Rebase on main")
	shipSettle(s, t, "the rebase", func() bool { return clickable(s, "Force push") })
	s.WantFrame("1 rewritten")
	s.ClickOn("Force push")
	head := strings.TrimSpace(git(t, blog, "rev-parse", "HEAD"))
	shipSettle(s, t, "the push", func() bool {
		return strings.HasPrefix(git(t, blog, "ls-remote", "origin", "feature"), head)
	})
	shipSettle(s, t, "Push again", func() bool { sh, _ := x.ShipOf(id, "blog"); return sh.Replaced == 0 && !clickable(s, "Force push") })
}

// A commit made after the branch was pushed, by the agent or a terminal, is pushable at once: the panel reads it, and
// Push comes back.
func TestGitPanelPushesACommitMadeAfterAPush(t *testing.T) {
	s, x, id, blog, _ := gitPanelScene(t, func(dir string) {
		git(t, dir, "checkout", "-q", "-b", "feature")
		writeFiles(t, dir, map[string]string{"post.md": "# Two\n"})
		git(t, dir, "commit", "-qam", "two")
		git(t, dir, "push", "-q", "-u", "origin", "feature")
	})
	shipSettle(s, t, "the branch pushed", func() bool { sh, _ := x.ShipOf(id, "blog"); return sh.Branch == "feature" && sh.Pushed })
	writeFiles(t, blog, map[string]string{"post.md": "# Three\n"})
	git(t, blog, "commit", "-qam", "three")
	shipSettle(s, t, "Push for the new commit", func() bool { return clickable(s, "Push") })
}
