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
	if dirty, err := wt.Dirty(ctx); dirty || err != nil {
		t.Errorf("a fresh worktree: dirty %v, %v", dirty, err)
	}
	if err := r.RemoveWorktree(ctx, dir, false); err != nil {
		t.Fatal(err)
	}
}

func TestAddWorktreeDetached(t *testing.T) {
	ctx := context.Background()
	_, clone := upstream(t)
	r := gitx.Repo{Dir: clone.Dir}
	dir := filepath.Join(t.TempDir(), "pinned")
	if err := r.AddWorktree(ctx, dir, "", "main"); err != nil {
		t.Fatal(err)
	}
	if got, _ := (gitx.Repo{Dir: dir}).Branch(ctx); got != "HEAD" {
		t.Errorf("a detached worktree is on %q", got)
	}
}

// ref: oracle git — git-check-ref-format(1) --branch: what git takes as a new branch's name.
func TestValidBranch(t *testing.T) {
	ctx := context.Background()
	for _, ok := range []string{"tester/eng-2231-round", "fix-1", "a/b/c"} {
		if err := gitx.ValidBranch(ctx, ok); err != nil {
			t.Errorf("ValidBranch(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "has space", "two..dots", "ends.lock", "-leading", "tilde~1", "trailing/"} {
		if err := gitx.ValidBranch(ctx, bad); err == nil {
			t.Errorf("ValidBranch(%q) took it", bad)
		}
	}
}

// ref: oracle git — git-clone(1). Clone makes a checkout of a URL, here a bare repo; a failure says what git said,
// and never waits on a prompt for credentials.
func TestClone(t *testing.T) {
	ctx := context.Background()
	origin, _ := upstream(t)
	bare := filepath.Join(t.TempDir(), "origin.git")
	origin.Git("clone", "-q", "--bare", origin.Dir, bare)
	dir := filepath.Join(t.TempDir(), "src", "pricing")
	if err := gitx.Clone(ctx, bare, dir); err != nil {
		t.Fatal(err)
	}
	r := gitx.Repo{Dir: dir}
	if got, err := r.Origin(ctx); err != nil || got != bare {
		t.Errorf("the clone's origin is %q, want %q", got, bare)
	}
	if !gitx.IsRepo(dir) {
		t.Error("no work tree")
	}
	err := gitx.Clone(ctx, filepath.Join(t.TempDir(), "nowhere.git"), filepath.Join(t.TempDir(), "x"))
	if err == nil || !strings.Contains(err.Error(), "nowhere.git") || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("cloning what isn't there: %v", err)
	}
}

// git-merge(1): "--ff-only: Only update to the new history if there is no divergent local history." A branch
// that can't be fast-forwarded is still the ref to cut from, and SyncBranch says why it isn't current.
func TestSyncBranchSaysWhenItCantFastForward(t *testing.T) {
	ctx := context.Background()
	_, clone := upstream(t)
	clone.Write(map[string]string{"b.txt": "mine\n"})
	clone.Commit("Ada", "local")
	r := gitx.Repo{Dir: clone.Dir}
	ref, err := r.SyncBranch(ctx, "main")
	if ref != "main" || err == nil || !strings.Contains(err.Error(), "merge") {
		t.Errorf("checked out and diverged: %q, %v", ref, err)
	}
	clone.Git("checkout", "-q", "-b", "other")
	ref, err = r.SyncBranch(ctx, "main")
	if ref != "main" || err == nil {
		t.Errorf("not checked out and diverged: %q, %v", ref, err)
	}
	clone.Git("checkout", "-q", "main")
	clone.Git("reset", "-q", "--hard", "HEAD~1")
	if ref, err := r.SyncBranch(ctx, "main"); ref != "main" || err != nil {
		t.Errorf("behind origin: %q, %v", ref, err)
	}
}

// Dirty says what git status says, and why it can't: a status that fails isn't clean, since what it guards (removing
// a worktree, switching branches) would lose work it couldn't see.
func TestDirty(t *testing.T) {
	ctx := context.Background()
	r := gittest.New(t)
	r.Write(map[string]string{"a.txt": "a\n"})
	r.Commit("Ada <ada@example.com>", "first")
	repo := gitx.Repo{Dir: r.Dir}
	if dirty, err := repo.Dirty(ctx); dirty || err != nil {
		t.Errorf("a clean tree: dirty %v, %v", dirty, err)
	}
	r.Write(map[string]string{"a.txt": "b\n"})
	if dirty, err := repo.Dirty(ctx); !dirty || err != nil {
		t.Errorf("a changed tree: dirty %v, %v", dirty, err)
	}
	if _, err := (gitx.Repo{Dir: t.TempDir()}).Dirty(ctx); err == nil {
		t.Error("a directory that isn't a repo reads as clean, with no error")
	}
}
