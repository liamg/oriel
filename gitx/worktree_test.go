package gitx_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/gitx/gittest"
)

// upstream is a repo and a clone of it whose origin has moved on since the clone: what SyncedBase must catch up.
func upstream(t *testing.T) (origin, clone *gittest.Repo) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	origin = gittest.New(t)
	origin.Write(map[string]string{"a.txt": "one\n"})
	origin.Commit("Ada", "first")
	clone = &gittest.Repo{T: t, Dir: t.TempDir(), When: origin.When}
	clone.Git("clone", "-q", origin.Dir, ".")
	origin.Write(map[string]string{"a.txt": "two\n"})
	origin.Commit("Ada", "second")
	return origin, clone
}

// ref: oracle git — git-worktree(1), git-fetch(1); what each call leaves is read back with git itself.
func TestSyncedBaseFetchesAndFastForwards(t *testing.T) {
	ctx := context.Background()
	origin, clone := upstream(t)
	r := gitx.Repo{Dir: clone.Dir}
	if got := r.DefaultBranch(ctx); got != "origin/main" {
		t.Errorf("DefaultBranch = %q, want origin/main", got)
	}
	base := r.SyncedBase(ctx)
	if base != "main" {
		t.Fatalf("SyncedBase = %q, want main", base)
	}
	// main is checked out in the clone, so it was fast-forwarded there.
	want := strings.TrimSpace(origin.Git("rev-parse", "main"))
	if got := strings.TrimSpace(clone.Git("rev-parse", "main")); got != want {
		t.Errorf("main is at %s, origin's at %s", got, want)
	}
}

func TestAddWorktreeOnAFreshBranch(t *testing.T) {
	ctx := context.Background()
	_, clone := upstream(t)
	r := gitx.Repo{Dir: clone.Dir}
	clone.Git("branch", "liam/x")
	branch := r.FreshBranch(ctx, "liam/x")
	if branch != "liam/x-2" {
		t.Fatalf("FreshBranch = %q, want liam/x-2", branch)
	}
	dir := filepath.Join(t.TempDir(), "repos", "clone")
	if err := r.AddWorktree(ctx, dir, branch, r.SyncedBase(ctx)); err != nil {
		t.Fatal(err)
	}
	wt := gitx.Repo{Dir: dir}
	if got, _ := wt.Branch(ctx); got != "liam/x-2" {
		t.Errorf("worktree is on %q", got)
	}
	if got := r.BranchWorktree(ctx, "liam/x-2"); got != dir {
		if ev, _ := filepath.EvalSymlinks(dir); got != ev {
			t.Errorf("BranchWorktree = %q, want %q", got, dir)
		}
	}
	if got := wt.MainClone(ctx); got != clone.Dir {
		if ev, _ := filepath.EvalSymlinks(clone.Dir); got != ev {
			t.Errorf("MainClone = %q, want %q", got, clone.Dir)
		}
	}
	if wt.Dirty(ctx) {
		t.Error("a fresh worktree is dirty")
	}
	if err := r.RemoveWorktree(ctx, dir, false); err != nil {
		t.Fatal(err)
	}
}

func TestAddWorktreeDetached(t *testing.T) {
	ctx := context.Background()
	_, clone := upstream(t)
	r := gitx.Repo{Dir: clone.Dir}
	dir := filepath.Join(t.TempDir(), "tour")
	if err := r.AddWorktree(ctx, dir, "", "main"); err != nil {
		t.Fatal(err)
	}
	if got, _ := (gitx.Repo{Dir: dir}).Branch(ctx); got != "HEAD" {
		t.Errorf("a detached worktree is on %q", got)
	}
}
