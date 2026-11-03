package gitx_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/gitx/gittest"
)

// mergeFixture is a checkout on main and a worktree of it on feature, cut from main.
func mergeFixture(t *testing.T) (main, feature *gittest.Repo) {
	t.Helper()
	gittest.Isolate(t)
	main = gittest.New(t)
	main.Write(map[string]string{"a.txt": "one\n"})
	main.Commit("Ada", "first")
	feature = &gittest.Repo{T: t, Dir: filepath.Join(t.TempDir(), "feature"), When: main.When}
	main.Git("worktree", "add", "-q", "-b", "feature", feature.Dir)
	return main, feature
}

func parents(r *gittest.Repo, ref string) []string {
	return strings.Fields(r.Git("rev-list", "--parents", "-n", "1", ref))[1:]
}

// ref: oracle git — git-merge(1): "When the merge resolves as a fast-forward, only update the branch pointer";
// git-merge-tree(1) --write-tree for a merge made without touching a work tree; git-worktree(1): a branch checked out
// in a work tree is moved there (git merge --ff-only), not under it.
func TestMergeIntoTheCheckedOutDefaultBranch(t *testing.T) {
	ctx := context.Background()
	main, feature := mergeFixture(t)
	r := gitx.Repo{Dir: feature.Dir}

	if _, err := r.MergeInto(ctx, "main", "feature", nil); !errors.Is(err, gitx.ErrUpToDate) {
		t.Fatalf("merging a branch with nothing new: %v", err)
	}

	feature.Write(map[string]string{"b.txt": "feature\n"})
	feature.Commit("Bob", "feature work")
	m, err := r.MergeInto(ctx, "main", "feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !m.FastForward || m.New != rev(feature, "feature") || rev(main, "main") != m.New {
		t.Fatalf("a fast-forward: %+v, main at %s", m, rev(main, "main"))
	}
	if got, _ := os.ReadFile(filepath.Join(main.Dir, "b.txt")); string(got) != "feature\n" {
		t.Errorf("main's checkout wasn't moved with it: b.txt is %q", got)
	}

	main.Write(map[string]string{"c.txt": "main\n"})
	main.Commit("Ada", "main work")
	feature.Write(map[string]string{"d.txt": "more\n"})
	feature.Commit("Bob", "more feature work")
	before := rev(main, "main")
	m, err = r.MergeInto(ctx, "main", "feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.FastForward || m.Old != before || rev(main, "main") != m.New {
		t.Fatalf("a merge commit: %+v", m)
	}
	if p := parents(main, "main"); len(p) != 2 || p[0] != before || p[1] != rev(feature, "feature") {
		t.Errorf("the merge's parents are %v", p)
	}
	if got := strings.TrimSpace(main.Git("log", "-1", "--format=%s", "main")); got != "Merge branch 'feature' into main" {
		t.Errorf("the merge's subject is %q", got)
	}
	if out := main.Git("status", "--porcelain"); out != "" {
		t.Errorf("main's checkout isn't clean after: %q", out)
	}
	for _, f := range []string{"c.txt", "d.txt"} {
		if _, err := os.Stat(filepath.Join(main.Dir, f)); err != nil {
			t.Errorf("main's checkout hasn't %s: %v", f, err)
		}
	}

	feature.Write(map[string]string{"e.txt": "later\n"})
	feature.Commit("Bob", "later")
	main.Write(map[string]string{"a.txt": "edited, not committed\n"})
	before = rev(main, "main")
	if _, err := r.MergeInto(ctx, "main", "feature", nil); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("merging into a dirty checkout: %v", err)
	}
	if rev(main, "main") != before {
		t.Error("main moved under a dirty checkout")
	}
	main.Git("checkout", "-q", "--", "a.txt")

	main.Write(map[string]string{"e.txt": "theirs\n"})
	main.Commit("Ada", "clash")
	before = rev(main, "main")
	_, err = r.MergeInto(ctx, "main", "feature", nil)
	if !errors.Is(err, gitx.ErrMergeConflict) || !strings.Contains(err.Error(), "e.txt") {
		t.Errorf("a conflicting merge: %v", err)
	}
	if rev(main, "main") != before || main.Git("status", "--porcelain") != "" {
		t.Error("a conflicting merge changed main")
	}
}

// ref: oracle git — git-update-ref(1) <newvalue> <oldvalue>: "verifies that the current value of the <ref> matches
// <oldvalue>" before it's moved; a branch no work tree has checked out is moved by its ref alone.
func TestMergeIntoABranchNotCheckedOut(t *testing.T) {
	ctx := context.Background()
	main, feature := mergeFixture(t)
	main.Git("checkout", "-q", "-b", "elsewhere")
	main.Write(map[string]string{"x.txt": "elsewhere\n"})
	main.Commit("Ada", "elsewhere")
	feature.Write(map[string]string{"b.txt": "feature\n"})
	feature.Commit("Bob", "feature work")
	main.Git("checkout", "-q", "main")
	main.Write(map[string]string{"c.txt": "main\n"})
	main.Commit("Ada", "main work")
	main.Git("checkout", "-q", "elsewhere")

	before := rev(main, "main")
	m, err := gitx.Repo{Dir: feature.Dir}.MergeInto(ctx, "main", "feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.FastForward || rev(main, "main") != m.New || m.Tree != "" {
		t.Fatalf("a merge into a branch not checked out: %+v", m)
	}
	if p := parents(main, "main"); len(p) != 2 || p[0] != before {
		t.Errorf("the merge's parents are %v", p)
	}
	if got := strings.TrimSpace(main.Git("rev-parse", "--abbrev-ref", "HEAD")); got != "elsewhere" {
		t.Errorf("the checkout moved to %q", got)
	}
	if _, err := os.Stat(filepath.Join(main.Dir, "b.txt")); !os.IsNotExist(err) {
		t.Error("the checkout on another branch got the merge's files")
	}
	if _, err := (gitx.Repo{Dir: feature.Dir}).MergeInto(ctx, "nope", "feature", nil); err == nil {
		t.Error("merged into a branch that isn't there")
	}
}
