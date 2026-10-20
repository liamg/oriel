package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// gitPanelScene is a directory session on blog, a checkout with origin on GitHub (a fake), its Git panel open and
// read. prepare runs on the checkout before it's opened.
func gitPanelScene(t *testing.T, prepare func(dir string)) (s *errScene, x *shipPlugin, id core.SessionID, blog, upstream string) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "You")
	t.Setenv("GIT_AUTHOR_EMAIL", "you@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "You")
	t.Setenv("GIT_COMMITTER_EMAIL", "you@example.com")
	hub := ghtest.New(t)
	lw := newHostWith(t, host.Options{GitHub: func(string, []string) gh.Client { return hub.Client() }})
	blog, upstream = shipRepo(t, "blog", map[string]string{"post.md": "# One\n"})
	if prepare != nil {
		prepare(blog)
	}
	s = newSceneWith(t, "", "", mockupSize, lw.connect(t))
	waitState(t, s.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.run("directory.open", registry.Args{"path": blog})
	st := waitState(t, s.store, "the directory's session in front", func(st core.State) bool {
		se, ok := st.Session(st.Focus)
		return ok && se.Snapshot().Kind == core.KindDirect
	})
	id = st.Focus
	s.until(t, "the window on the directory", func() bool { s.sh.runTodo(); return s.reopen() })
	x = s.ship()
	s.run("project.panel", registry.Args{"name": "git"})
	s.shipSettle(t, "the Git panel", func() bool { return strings.Contains(s.frameText(), "→ main") })
	return s, x, id, blog, upstream
}

// clickable says the frame has an enabled button named label.
func (s *errScene) clickable(label string) bool {
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
	return find(s.ui.AccessTree())
}

// framesUntil frames, with what the loop was handed, until ok; each frame is checked first.
func (s *errScene) framesUntil(t *testing.T, what string, check func(frame int), ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for n := 0; ; n++ {
		s.sh.runTodo()
		s.frame()
		check(n)
		if ok() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s:\n%s", what, s.frameText())
		}
	}
}

// ui.md, Loading: "No screen looks empty, broken or finished while its data is on the way", and "Ship's step actions
// are **Gate**": "submit busy with the reason". From the click until the panel's read shows the commit, Commit stays
// busy and the form isn't offered again.
func TestGitPanelCommitStaysBusyUntilItShows(t *testing.T) {
	s, x, id, blog, _ := gitPanelScene(t, func(dir string) {
		writeFiles(t, dir, map[string]string{"post.md": "# Two\n"})
	})
	s.shipSettle(t, "the change", func() bool { return strings.Contains(s.frameText(), "CHANGES · 1") })
	s.typeInto(t, "Commit message", "docs: two")
	s.click(centre(s.access("Commit").Bounds))
	busy := false
	s.framesUntil(t, "the commit", func(n int) {
		text := s.frameText()
		if strings.Contains(text, "Committing…") {
			busy = true
		}
		if s.clickable("Commit") {
			t.Fatalf("frame %d after the click shows Commit to press again:\n%s", n, text)
		}
		if n == 0 && !strings.Contains(text, "Committing…") {
			t.Fatalf("the first frame after the click doesn't say it's committing:\n%s", text)
		}
		if strings.Contains(text, "Committing…") {
			if m := s.access("Commit message"); !m.Disabled {
				t.Fatalf("frame %d: the message can be typed into while committing", n)
			}
		}
	}, func() bool {
		text := s.frameText()
		return strings.Contains(text, "Nothing uncommitted.") && strings.Contains(text, "TO PUSH · 1") && !strings.Contains(text, "Committing…")
	})
	if !busy {
		t.Error("no frame said it was committing")
	}
	if got := strings.TrimSpace(git(t, blog, "log", "-1", "--format=%s")); got != "docs: two" {
		t.Errorf("the last commit is %q", got)
	}
	if sh, _ := x.ship(id, "blog"); len(sh.Files) != 0 {
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
	s.shipSettle(t, "the change", func() bool { return strings.Contains(s.frameText(), "CHANGES · 1") })
	s.typeInto(t, "Commit message", "docs: two")
	s.click(centre(s.access("Commit").Bounds))
	s.shipSettle(t, "the failure", func() bool { return strings.Contains(s.frameText(), "no commits today") })
	s.wantFrame(t, "CHANGES · 1", "docs: two")
	if !s.clickable("Commit") {
		t.Errorf("Commit isn't back:\n%s", s.frameText())
	}
	if m := s.access("Commit message"); m.Disabled {
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
	s.shipSettle(t, "the branch", func() bool { sh, _ := x.ship(id, "blog"); return sh.Branch == "feature" && sh.Default == "main" })
	main := strings.TrimSpace(git(t, blog, "rev-parse", "main"))

	s.clickAccess(t, "More git actions")
	s.clickAccess(t, "Merge into main…")
	var ask core.Notification
	for _, n := range s.store.State().Notifications {
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
	s.run(ask.Actions[0].Command, registry.Args(ask.Actions[0].Args))
	s.shipSettle(t, "the merge", func() bool {
		return strings.TrimSpace(git(t, blog, "rev-parse", "main")) == strings.TrimSpace(git(t, blog, "rev-parse", "feature"))
	})
	if got := strings.TrimSpace(git(t, blog, "rev-parse", "--abbrev-ref", "HEAD")); got != "feature" {
		t.Errorf("the checkout moved to %q", got)
	}
	if out := git(t, blog, "ls-remote", "origin", "main"); strings.Contains(out, strings.TrimSpace(git(t, blog, "rev-parse", "main"))) {
		t.Error("the merge was pushed")
	}

	s.run("ship.merge-default", registry.Args{"confirm": true})
	s.shipSettle(t, "the second merge's answer", func() bool { return strings.Contains(s.frameText(), "nothing to merge") })
}

// On the default branch itself there's nothing to merge: the item is off.
func TestGitPanelMergeIsOffOnTheDefaultBranch(t *testing.T) {
	s, _, _, _, _ := gitPanelScene(t, nil)
	s.clickAccess(t, "More git actions")
	if n := s.access("Merge into main…"); !n.Disabled {
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
	s.shipSettle(t, "the branch behind main", func() bool { sh, _ := x.ship(id, "blog"); return sh.Branch == "feature" && sh.Behind == 1 })
	s.click(centre(s.access("Rebase on main").Bounds))
	s.shipSettle(t, "the rebase", func() bool { return s.clickable("Force push") })
	s.wantFrame(t, "1 rewritten")
	s.click(centre(s.access("Force push").Bounds))
	head := strings.TrimSpace(git(t, blog, "rev-parse", "HEAD"))
	s.shipSettle(t, "the push", func() bool {
		return strings.HasPrefix(git(t, blog, "ls-remote", "origin", "feature"), head)
	})
	s.shipSettle(t, "Push again", func() bool { sh, _ := x.ship(id, "blog"); return sh.Replaced == 0 && !s.clickable("Force push") })
}
