package gitui_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/gitx"
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
	s.Until("git status to see "+what, ok)
}

// scriptedGitScene is gitScene with file watchers that see only what the test says changed (ScriptedFiles).
func scriptedGitScene(t *testing.T) (*apptest.Scene, *gittest.Repo) {
	t.Helper()
	repo := gittest.New(t)
	repo.Write(map[string]string{"main.go": "package main\n", ".gitignore": "build/\n", "node_modules/x/index.js": "x\n", "build/out": "x\n"})
	repo.Commit("Ada", "first")
	s := apptest.New(t, apptest.Options{WorkDir: repo.Dir, ScriptedFiles: true})
	gitOf(s).Wait()
	s.Idle()
	return s, repo
}

// The work tree's status is read again when the tree or .git changes, and not while nothing does.
func TestGitStatusFollowsTheTree(t *testing.T) {
	runs := gittest.CountGit(t)
	s, repo := scriptedGitScene(t)
	read := runs("status")
	s.Now = s.Now.Add(time.Minute)
	s.Idle()
	if n := runs("status") - read; n > 0 {
		t.Fatalf("git status ran %d times with nothing changed", n)
	}
	s.WriteFiles(repo.Dir, map[string]string{"node_modules/x/more.js": "y\n", "build/more": "y\n"})
	s.Idle()
	if n := runs("status") - read; n > 0 {
		t.Fatalf("git status ran %d times for node_modules and an ignored folder", n)
	}
	s.WriteFiles(repo.Dir, map[string]string{"new.go": "package main\n"})
	waitGit(t, s, "new.go", func() bool { _, ok := gitOf(s).State().File("new.go"); return ok })
	repo.Git("checkout", "-q", "-b", "other")
	git := filepath.Join(repo.Dir, ".git")
	s.FilesChanged(filepath.Join(git, "HEAD"), filepath.Join(git, "refs", "heads", "other"), filepath.Join(git, "logs", "HEAD"))
	waitGit(t, s, "the branch change", func() bool { return gitOf(s).State().Branch == "other" })
}

// git's own housekeeping under .git (another work tree's admin folder, fsmonitor's cookies, FETCH_HEAD, logs)
// doesn't read the status again, nor does another work tree busy in its own files.
func TestGitStatusIgnoresGitsHousekeeping(t *testing.T) {
	runs := gittest.CountGit(t)
	repo := gittest.New(t)
	repo.Write(map[string]string{"main.go": "package main\n"})
	repo.Commit("Ada", "first")
	other := filepath.Join(t.TempDir(), "other")
	repo.Git("worktree", "add", "-q", "-b", "other", other)
	git := filepath.Join(repo.Dir, ".git")
	os.MkdirAll(filepath.Join(git, "fsmonitor--daemon", "cookies"), 0o755)
	s := apptest.New(t, apptest.Options{WorkDir: repo.Dir, ScriptedFiles: true})
	gitOf(s).Wait()
	s.Idle()
	read := runs("status")
	for i := range 3 {
		cookie := filepath.Join(git, "fsmonitor--daemon", "cookies", fmt.Sprint(i))
		paths := []string{filepath.Join(other, "main.go"), filepath.Join(git, "worktrees", "other", "index"), cookie,
			filepath.Join(git, "FETCH_HEAD"), filepath.Join(git, "logs", "HEAD")}
		os.WriteFile(paths[0], []byte(fmt.Sprintf("package main // %d\n", i)), 0o644)
		os.WriteFile(paths[1], []byte("x"), 0o644)
		os.WriteFile(cookie, nil, 0o644)
		os.Remove(cookie)
		os.WriteFile(paths[3], []byte(fmt.Sprint(i)), 0o644)
		os.WriteFile(paths[4], []byte(fmt.Sprint(i)), 0o644)
		s.FilesChanged(paths...)
		s.Idle()
	}
	if n := runs("status") - read; n > 0 {
		t.Fatalf("git status ran %d times for git's housekeeping", n)
	}
}

// With no room for a watch the status is polled, as before.
func TestGitStatusPollsWithoutAWatch(t *testing.T) {
	s, repo := gitScene(t, func(cfg *apptest.Config) {
		cfg.FSWatch = func() (*fswatch.Watcher, error) { return nil, fswatch.ErrLimit }
	})
	os.WriteFile(filepath.Join(repo.Dir, "new.go"), []byte("package main\n"), 0o644)
	waitGit(t, s, "new.go", func() bool { _, ok := gitOf(s).State().File("new.go"); return ok })
}

// The work tree the window leaves keeps its status: back on it, the status is there at once, before git is asked
// again or its root looked for.
func TestTheWindowsTreeKeepsItsStatusAcrossAMove(t *testing.T) {
	s, repo := gitScene(t)
	var back *gitx.State
	cancel := kit.OnDirChanged(s.Registry(), "the window's tree on the way back", func(ev kit.DirChanged) {
		if ev.Dir == repo.Dir {
			back = kit.GitOf(s.Registry()).Tree("").State()
		}
	})
	defer cancel.Dispose()
	move := func(to string) {
		t.Helper()
		win, _ := kit.WindowOf(s.Registry())
		if err := win.Reopen(to, ""); err != nil {
			t.Fatal(err)
		}
		s.Reopen()
	}
	move(t.TempDir())
	move(repo.Dir)
	if back == nil || back.Branch != "main" {
		t.Errorf("back on the repo, its status is %+v", back)
	}
}
