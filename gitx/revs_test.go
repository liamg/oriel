package gitx_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/gitx/gittest"
)

// ref: oracle git — git-rev-list(1), git-merge-base(1), git-checkout(1); each answer is read back with git itself.
func TestRevsCountAndMove(t *testing.T) {
	gittest.Isolate(t)
	ctx := context.Background()
	r := gittest.New(t)
	r.Write(map[string]string{"a.txt": "one\n"})
	r.Commit("Ada", "first")
	first := strings.TrimSpace(r.Git("rev-parse", "HEAD"))
	g := gitx.Repo{Dir: r.Dir}
	if err := g.CheckoutNew(ctx, "feat", "main"); err != nil {
		t.Fatal(err)
	}
	if b := g.CurrentBranch(ctx); b != "feat" {
		t.Errorf("CurrentBranch = %q, want feat", b)
	}
	r.Write(map[string]string{"b.txt": "two\n"})
	r.Commit("Ada", "second")
	r.Write(map[string]string{"c.txt": "three\n"})
	r.Commit("Ada", "third")
	if n, err := g.CountCommits(ctx, "HEAD", "main"); err != nil || n != 2 {
		t.Errorf("CountCommits(HEAD --not main) = %d, %v; want 2", n, err)
	}
	if n, err := g.CountCommits(ctx, "HEAD"); err != nil || n != 3 {
		t.Errorf("CountCommits(HEAD) = %d, %v; want 3", n, err)
	}
	if mb, err := g.MergeBaseOf(ctx, "HEAD", "main"); err != nil || mb != first {
		t.Errorf("MergeBaseOf = %q, %v; want %s", mb, err, first)
	}
	if !g.Verify(ctx, "main") || g.Verify(ctx, "nope") {
		t.Error("Verify should know main and not nope")
	}
	if rev, err := g.Rev(ctx, "main"); err != nil || rev != first {
		t.Errorf("Rev(main) = %q, %v", rev, err)
	}
	r.Write(map[string]string{"a.txt": "changed\n"})
	if files, err := g.ChangedFiles(ctx, "main"); err != nil || !slices.Equal(files, []string{"a.txt", "b.txt", "c.txt"}) {
		t.Errorf("ChangedFiles(main) = %v, %v", files, err)
	}
	r.Git("checkout", "--", "a.txt")
	if err := g.Detach(ctx, first); err != nil {
		t.Fatal(err)
	}
	if b := g.CurrentBranch(ctx); b != "" {
		t.Errorf("detached, CurrentBranch = %q", b)
	}
	if head := strings.TrimSpace(r.Git("rev-parse", "HEAD")); head != first {
		t.Errorf("Detach left HEAD at %s", head)
	}
	if g.ObjectsKiB(ctx) <= 0 {
		t.Error("the object store has a size")
	}
}

// ref: oracle git — git-init(1), git-clone(1), git-fetch(1).
func TestInitCloneAndFetchRef(t *testing.T) {
	gittest.Isolate(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "fresh")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := gitx.Init(ctx, dir, "trunk"); err != nil {
		t.Fatal(err)
	}
	fresh := &gittest.Repo{T: t, Dir: dir}
	if head := strings.TrimSpace(fresh.Git("symbolic-ref", "HEAD")); head != "refs/heads/trunk" {
		t.Errorf("Init's HEAD is %s", head)
	}
	origin := gittest.New(t)
	origin.Write(map[string]string{"a.txt": "one\n"})
	origin.Commit("Ada", "first")
	origin.Write(map[string]string{"a.txt": "two\n"})
	origin.Commit("Ada", "second")
	clone := filepath.Join(t.TempDir(), "clone")
	if err := gitx.CloneDepth(ctx, "file://"+origin.Dir, clone, 1); err != nil {
		t.Fatal(err)
	}
	c := &gittest.Repo{T: t, Dir: clone}
	if n := strings.TrimSpace(c.Git("rev-list", "--count", "HEAD")); n != "1" {
		t.Errorf("a depth-1 clone has %s commits", n)
	}
	origin.Git("update-ref", "refs/pull/7/head", "HEAD~1")
	if err := (gitx.Repo{Dir: clone}).FetchRef(ctx, "refs/pull/7/head", "refs/oriel/pr/7"); err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(origin.Git("rev-parse", "HEAD~1"))
	if got := strings.TrimSpace(c.Git("rev-parse", "refs/oriel/pr/7")); got != want {
		t.Errorf("FetchRef kept %s, want %s", got, want)
	}
}

// ref: oracle git — git-config(1): user.name, read where no repo is.
func TestUserName(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte("[user]\n\tname = Ada Lovelace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	if got := gitx.UserName(context.Background()); got != "Ada Lovelace" {
		t.Errorf("UserName = %q", got)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	if got := gitx.UserName(context.Background()); got != "" {
		t.Errorf("with no name set, UserName = %q", got)
	}
}
