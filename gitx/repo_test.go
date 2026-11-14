package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/gitx/gittest"
)

// fixture is a repo with commits by two authors, an ignored file, an untracked one, and a branch one ahead of its
// upstream. git is the oracle: what the functions return is what git says.
func fixture(t *testing.T) Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), append([]string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null"}, env...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	commit := func(who, msg string, at time.Time) {
		d := at.Format(time.RFC3339)
		run([]string{"GIT_AUTHOR_NAME=" + who, "GIT_AUTHOR_EMAIL=" + who + "@x", "GIT_COMMITTER_NAME=" + who,
			"GIT_COMMITTER_EMAIL=" + who + "@x", "GIT_AUTHOR_DATE=" + d, "GIT_COMMITTER_DATE=" + d}, "commit", "-qm", msg)
	}
	run(nil, "init", "-q", "-b", "main")
	now := time.Now()
	write(".gitignore", "build/\n")
	write("main.go", "package main\n")
	run(nil, "add", ".")
	commit("Ada", "first", now.Add(-48*time.Hour))
	write("lib/a.go", "package lib\n")
	run(nil, "add", ".")
	commit("Bob", "second", now.Add(-24*time.Hour))
	run(nil, "branch", "upstream")
	run(nil, "branch", "--set-upstream-to=upstream")
	write("lib/b.go", "package lib\n")
	run(nil, "add", ".")
	commit("Ada", "third", now.Add(-time.Hour))
	write("build/out.bin", "x")
	write("notes.txt", "untracked\n")
	return Repo{Dir: dir}
}

func TestRepo(t *testing.T) {
	r := fixture(t)
	ctx := context.Background()
	if !IsRepo(r.Dir) || IsRepo(t.TempDir()) {
		t.Error("IsRepo")
	}
	files, err := r.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(files, " "), ".gitignore lib/a.go lib/b.go main.go notes.txt"; got != want {
		t.Errorf("Files = %q, want %q", got, want)
	}
	if b, err := r.Branch(ctx); err != nil || b != "main" {
		t.Errorf("Branch = %q, %v", b, err)
	}
	if ab, err := r.AheadBehind(ctx); err != nil || ab.Ahead != 1 || ab.Behind != 0 || ab.Upstream != "upstream" {
		t.Errorf("AheadBehind = %+v, %v", ab, err)
	}
	times, err := r.CommitTimes(ctx, time.Now().Add(-36*time.Hour))
	if err != nil || len(times) != 2 {
		t.Errorf("CommitTimes since 36h = %v, %v", times, err)
	}
	last, err := r.Last(ctx)
	if err != nil || last.Subject != "third" || last.Author != "Ada" || len(last.Hash) != 7 {
		t.Errorf("Last = %+v, %v", last, err)
	}
	authors, err := r.Authors(ctx)
	if err != nil || len(authors) != 2 || authors[0] != (Author{"Ada", 2}) || authors[1] != (Author{"Bob", 1}) {
		t.Errorf("Authors = %+v, %v", authors, err)
	}
}

func TestNoUpstream(t *testing.T) {
	r := fixture(t)
	exec.Command("git", "-C", r.Dir, "branch", "--unset-upstream").Run()
	if ab, err := r.AheadBehind(context.Background()); err != nil || ab.Upstream != "" {
		t.Errorf("AheadBehind with no upstream = %+v, %v", ab, err)
	}
}

// A write that finds the index locked by another git waits for it to let go, rather than failing.
func TestWriteWaitsForTheIndexLock(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"a.txt": "a\n"})
	lock := filepath.Join(r.Dir, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	// The other git lets go just after ours has found the index locked.
	bin := t.TempDir()
	script := "#!/bin/sh\n'" + real + "' \"$@\" && exit 0\nrm -f '" + lock + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := (Repo{Dir: r.Dir}).Stage(context.Background(), "a.txt"); err != nil {
		t.Fatalf("staging past a lock let go: %v", err)
	}
}

// A lock left behind by a git that died is removed once it's old and no git is running.
func TestWriteRemovesAStaleIndexLock(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"a.txt": "a\n"})
	lock := staleIndexLock(t, r.Dir)
	if err := (Repo{Dir: r.Dir, gitRunning: func() bool { return false }}).Stage(context.Background(), "a.txt"); err != nil {
		t.Fatalf("staging past a stale lock: %v", err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Errorf("the stale lock is still there: %v", err)
	}
}

// An old lock is kept while a git runs: it may be a commit waiting on its editor.
func TestWriteKeepsALockWhileGitRuns(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"a.txt": "a\n"})
	lock := staleIndexLock(t, r.Dir)
	if err := (Repo{Dir: r.Dir, gitRunning: func() bool { return true }}).Stage(context.Background(), "a.txt"); err == nil {
		t.Fatal("staged past a lock a running git may hold")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("the lock was removed: %v", err)
	}
}

func staleIndexLock(t *testing.T, dir string) string {
	t.Helper()
	lock := filepath.Join(dir, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleLock)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	return lock
}

// A git stopped by its context removes its lock on the way out.
func TestCancelledGitLetsGoOfTheIndex(t *testing.T) {
	gittest.Isolate(t)
	r := gittest.New(t)
	r.Write(map[string]string{"a.txt": "a\n"})
	r.Git("add", "a.txt")
	hook := filepath.Join(r.Dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nsleep 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := (Repo{Dir: r.Dir}).Commit(ctx, "a", CommitOpts{}); err == nil {
		t.Fatal("the commit finished before it was stopped")
	}
	if _, err := os.Stat(filepath.Join(r.Dir, ".git", "index.lock")); !os.IsNotExist(err) {
		t.Errorf("the stopped git left its lock: %v", err)
	}
}
