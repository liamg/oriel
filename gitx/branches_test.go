package gitx_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/gitx/gittest"
)

// originFixture is a bare origin with main and other pushed from seed, and a clone of it on main with a local other.
func originFixture(t *testing.T) (seed, clone *gittest.Repo, bare string) {
	t.Helper()
	gittest.Isolate(t)
	bare = filepath.Join(t.TempDir(), "origin.git")
	seed = gittest.New(t)
	seed.Git("init", "-q", "--bare", "-b", "main", bare)
	seed.Write(map[string]string{"a.txt": "one\n"})
	seed.Commit("Ada", "first")
	seed.Git("remote", "add", "origin", bare)
	seed.Git("checkout", "-q", "-b", "other")
	seed.Write(map[string]string{"b.txt": "other\n"})
	seed.Commit("Ada", "other work")
	seed.Git("checkout", "-q", "main")
	seed.Git("push", "-q", "origin", "main", "other")
	clone = &gittest.Repo{T: t, Dir: filepath.Join(t.TempDir(), "clone"), When: seed.When}
	seed.Git("clone", "-q", bare, clone.Dir)
	clone.Git("branch", "-q", "other", "origin/other")
	return seed, clone, bare
}

func rev(r *gittest.Repo, ref string) string { return strings.TrimSpace(r.Git("rev-parse", ref)) }

func samePath(a, b string) bool {
	ea, _ := filepath.EvalSymlinks(a)
	eb, _ := filepath.EvalSymlinks(b)
	return a == b || ea == eb
}

// ref: oracle git — git-for-each-ref(1) upstream:track "[gone]", git-fetch(1) --prune, git-branch(1) --merged.
func TestBranchesMergedAndGone(t *testing.T) {
	ctx := context.Background()
	_, clone, bare := originFixture(t)
	r := gitx.Repo{Dir: clone.Dir}
	clone.Git("branch", "merged")
	clone.Git("checkout", "-q", "-b", "feature")
	clone.Write(map[string]string{"c.txt": "feature\n"})
	clone.Commit("Bob", "feature work")
	clone.Git("push", "-q", "-u", "origin", "feature")
	clone.Write(map[string]string{"c.txt": "more\n"})
	clone.Commit("Bob", "more feature work")
	clone.Git("checkout", "-q", "main")

	merged, err := r.MergedInto(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(merged, []string{"main", "merged"}) {
		t.Errorf("MergedInto(main) = %v", merged)
	}

	bs, err := r.Branches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, b := range bs {
		names = append(names, b.Name)
	}
	if !slices.Equal(names, []string{"feature", "main", "merged", "other"}) {
		t.Fatalf("Branches = %v", names)
	}
	f := bs[0]
	if f.Upstream != "origin/feature" || f.Gone || f.Head != rev(clone, "feature") || f.Subject != "more feature work" ||
		!f.When.Equal(clone.When.Add(-time.Minute)) {
		t.Errorf("feature = %+v", f)
	}
	if bs[2].Upstream != "" || bs[2].Gone {
		t.Errorf("merged has no upstream: %+v", bs[2])
	}
	if tr, err := r.BranchTracking(ctx, "feature"); err != nil || tr.Upstream != "origin/feature" || tr.Ahead != 1 || tr.Behind != 0 {
		t.Errorf("BranchTracking(feature) = %+v, %v", tr, err)
	}
	if tr, err := r.BranchTracking(ctx, "merged"); err != nil || tr != (gitx.Tracking{}) {
		t.Errorf("BranchTracking(merged) = %+v, %v", tr, err)
	}

	(&gittest.Repo{T: t, Dir: bare}).Git("branch", "-q", "-D", "feature")
	if err := r.FetchPrune(ctx); err != nil {
		t.Fatal(err)
	}
	bs, _ = r.Branches(ctx)
	if !bs[0].Gone || bs[0].Upstream != "origin/feature" {
		t.Errorf("after the upstream was deleted, feature = %+v", bs[0])
	}
	if bs[1].Gone {
		t.Errorf("main is gone: %+v", bs[1])
	}
}

// ref: oracle git — git-worktree(1) list --porcelain and prune.
func TestWorktreesAndPrune(t *testing.T) {
	ctx := context.Background()
	_, clone, _ := originFixture(t)
	r := gitx.Repo{Dir: clone.Dir}
	onBranch := filepath.Join(t.TempDir(), "wt")
	detached := filepath.Join(t.TempDir(), "detached")
	gone := filepath.Join(t.TempDir(), "gone")
	clone.Git("worktree", "add", "-q", "-b", "wt", onBranch, "main")
	clone.Git("worktree", "add", "-q", "--detach", detached, "main")
	clone.Git("worktree", "add", "-q", "-b", "gone", gone, "main")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	wts, err := r.Worktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 4 {
		t.Fatalf("Worktrees = %+v", wts)
	}
	head := rev(clone, "main")
	want := []gitx.Worktree{
		{Dir: clone.Dir, Branch: "main", Head: head},
		{Dir: onBranch, Branch: "wt", Head: head},
		{Dir: detached, Head: head, Detached: true},
		{Dir: gone, Branch: "gone", Head: head, Prunable: true},
	}
	for i, w := range wts {
		d := want[i].Dir
		want[i].Dir = w.Dir
		if !samePath(w.Dir, d) || w != want[i] {
			t.Errorf("worktree %d = %+v, want %+v at %s", i, w, want[i], d)
		}
	}

	if err := r.PruneWorktrees(ctx); err != nil {
		t.Fatal(err)
	}
	wts, _ = r.Worktrees(ctx)
	if len(wts) != 3 || slices.ContainsFunc(wts, func(w gitx.Worktree) bool { return w.Prunable }) {
		t.Errorf("after prune: %+v", wts)
	}
}

// ref: oracle git — git-worktree(1): a bare repo lists itself as "bare".
func TestWorktreesBare(t *testing.T) {
	_, _, bare := originFixture(t)
	wts, err := gitx.Repo{Dir: bare}.Worktrees(context.Background())
	if err != nil || len(wts) != 1 || !wts[0].Bare {
		t.Errorf("Worktrees of a bare repo = %+v, %v", wts, err)
	}
}

// ref: oracle git — git-branch(1): -d refuses a branch not merged, -D deletes it anyway.
func TestDeleteBranch(t *testing.T) {
	ctx := context.Background()
	_, clone, _ := originFixture(t)
	r := gitx.Repo{Dir: clone.Dir}
	clone.Git("checkout", "-q", "-b", "feature")
	clone.Write(map[string]string{"c.txt": "x\n"})
	clone.Commit("Bob", "unmerged")
	clone.Git("checkout", "-q", "main")
	err := r.DeleteBranch(ctx, "feature", false)
	if err == nil || !strings.Contains(err.Error(), "not fully merged") {
		t.Errorf("DeleteBranch -d of an unmerged branch: %v", err)
	}
	if !r.HasBranch(ctx, "feature") {
		t.Fatal("feature was deleted")
	}
	if err := r.DeleteBranch(ctx, "feature", true); err != nil {
		t.Fatal(err)
	}
	if r.HasBranch(ctx, "feature") {
		t.Error("feature is still there after -D")
	}
}

// ref: oracle git — git-pull(1) --ff-only for the branch checked out; git-fetch(1) <b>:<b> for one that isn't,
// which git refuses when it isn't a fast-forward.
func TestFastForward(t *testing.T) {
	ctx := context.Background()
	seed, clone, _ := originFixture(t)
	r := gitx.Repo{Dir: clone.Dir}
	seed.Write(map[string]string{"a.txt": "two\n"})
	seed.Commit("Ada", "second")
	seed.Git("checkout", "-q", "other")
	seed.Write(map[string]string{"b.txt": "other two\n"})
	seed.Commit("Ada", "other second")
	seed.Git("checkout", "-q", "main")
	seed.Git("push", "-q", "origin", "main", "other")

	clone.Write(map[string]string{"a.txt": "local edit\n"})
	err := r.FastForward(ctx, "main")
	if err == nil || err.Error() != "main has uncommitted changes: commit or stash them first" {
		t.Errorf("FastForward with a dirty tree: %v", err)
	}
	clone.Git("checkout", "-q", "--", "a.txt")
	if err := r.FastForward(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	if rev(clone, "main") != rev(seed, "main") {
		t.Error("main wasn't fast-forwarded")
	}
	if err := r.FastForward(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	if rev(clone, "other") != rev(seed, "other") {
		t.Error("other wasn't fast-forwarded")
	}

	clone.Write(map[string]string{"a.txt": "mine\n"})
	clone.Commit("Bob", "mine on main")
	clone.Git("checkout", "-q", "other")
	clone.Write(map[string]string{"b.txt": "mine\n"})
	clone.Commit("Bob", "mine on other")
	clone.Git("checkout", "-q", "main")
	seed.Write(map[string]string{"a.txt": "three\n"})
	seed.Commit("Ada", "third")
	seed.Git("checkout", "-q", "other")
	seed.Write(map[string]string{"b.txt": "three\n"})
	seed.Commit("Ada", "other third")
	seed.Git("checkout", "-q", "main")
	seed.Git("push", "-q", "origin", "main", "other")

	err = r.FastForward(ctx, "main")
	if err == nil || !strings.Contains(err.Error(), "Not possible to fast-forward") {
		t.Errorf("FastForward of a diverged main: %v", err)
	}
	err = r.FastForward(ctx, "other")
	if err == nil || !strings.Contains(err.Error(), "non-fast-forward") {
		t.Errorf("FastForward of a diverged other: %v", err)
	}
}
