package gitui_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/gitx/gittest"
)

// gitScene is the app on a fresh repo with node_modules and an ignored build/, its status read once.
func gitScene(t *testing.T, change ...func(*apptest.Config)) (*apptest.Scene, *gittest.Repo) {
	t.Helper()
	repo := gittest.New(t)
	repo.Write(map[string]string{"main.go": "package main\n", ".gitignore": "build/\n", "node_modules/x/index.js": "x\n", "build/out": "x\n"})
	repo.Commit("Ada", "first")
	s := apptest.New(t, apptest.Options{WorkDir: repo.Dir, Config: func(cfg *apptest.Config) {
		for _, f := range change {
			f(cfg)
		}
	}})
	gitOf(s).Wait()
	return s, repo
}

func waitGit(t *testing.T, s *apptest.Scene, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("git status never saw %s", what)
		}
	}
}

// The work tree's status is read again when the tree or .git changes, and not while nothing does.
func TestGitStatusFollowsTheTree(t *testing.T) {
	runs := gittest.CountGit(t)
	s, repo := gitScene(t)
	read := runs("status")
	time.Sleep(2500 * time.Millisecond)
	if n := runs("status") - read; n > 0 {
		t.Fatalf("git status ran %d times with nothing changed", n)
	}
	os.WriteFile(filepath.Join(repo.Dir, "node_modules", "x", "more.js"), []byte("y\n"), 0o644)
	os.WriteFile(filepath.Join(repo.Dir, "build", "more"), []byte("y\n"), 0o644)
	time.Sleep(time.Second)
	if n := runs("status") - read; n > 0 {
		t.Fatalf("git status ran %d times for node_modules and an ignored folder", n)
	}
	os.WriteFile(filepath.Join(repo.Dir, "new.go"), []byte("package main\n"), 0o644)
	waitGit(t, s, "new.go", func() bool { _, ok := gitOf(s).State().File("new.go"); return ok })
	repo.Git("checkout", "-q", "-b", "other")
	waitGit(t, s, "the branch change", func() bool { return gitOf(s).State().Branch == "other" })
}

// With no room for a watch the status is polled, as before.
func TestGitStatusPollsWithoutAWatch(t *testing.T) {
	s, repo := gitScene(t, func(cfg *apptest.Config) {
		cfg.FSWatch = func() (*fswatch.Watcher, error) { return nil, fswatch.ErrLimit }
	})
	os.WriteFile(filepath.Join(repo.Dir, "new.go"), []byte("package main\n"), 0o644)
	waitGit(t, s, "new.go", func() bool { _, ok := gitOf(s).State().File("new.go"); return ok })
}
