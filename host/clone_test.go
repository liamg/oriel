package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repos.clone clones a URL into the repos directory and registers it;
// a clone that fails says what git said.
func TestCloneRegistersTheRepo(t *testing.T) {
	r := newRig(t)
	r.w.opts.ReposDir = filepath.Join(t.TempDir(), "src")
	bare := filepath.Join(t.TempDir(), "pricing.git")
	r.repo.Git("clone", "-q", "--bare", r.repo.Dir, bare)
	ctx := context.Background()
	repo, err := r.w.CloneRepo(ctx, CloneReq{URL: bare})
	if err != nil {
		t.Fatal(err)
	}
	if repo.ID != "pricing" || repo.Path != filepath.Join(r.w.opts.ReposDir, "pricing") || repo.Remote != bare {
		t.Errorf("cloned %+v", repo)
	}
	if _, err := os.Stat(filepath.Join(repo.Path, "round.go")); err != nil {
		t.Errorf("the clone has no files: %v", err)
	}
	if got, ok := r.w.repos.Find("pricing"); !ok || got.Path != repo.Path {
		t.Errorf("not registered: %+v", r.w.repos.Repos())
	}
	again, err := r.w.CloneRepo(ctx, CloneReq{URL: bare})
	if err != nil || again.ID != repo.ID {
		t.Errorf("cloning the same URL again: %+v, %v", again, err)
	}
	named, err := r.w.CloneRepo(ctx, CloneReq{URL: bare, Name: "Pricing Two"})
	if err != nil || named.Path != filepath.Join(r.w.opts.ReposDir, "pricing-two") {
		t.Errorf("a clone with a name: %+v, %v", named, err)
	}
	if _, err := r.w.CloneRepo(ctx, CloneReq{URL: bare, Name: "pricing-two"}); err != nil {
		t.Errorf("a name taken by the same remote: %v", err)
	}
	os.MkdirAll(filepath.Join(r.w.opts.ReposDir, "taken"), 0o755)
	if _, err := r.w.CloneRepo(ctx, CloneReq{URL: bare, Name: "taken"}); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("cloning over a directory: %v", err)
	}
	_, err = r.w.CloneRepo(ctx, CloneReq{URL: filepath.Join(t.TempDir(), "gone.git")})
	if err == nil || !strings.Contains(err.Error(), "gone.git") || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("a URL that isn't there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.w.opts.ReposDir, "gone")); err == nil {
		t.Error("a failed clone left its directory")
	}
}

// A repo that isn't registered says how to get it.
func TestUnknownRepoSaysHowToGetIt(t *testing.T) {
	r := newRig(t)
	_, err := r.w.resolveRepo(context.Background(), "billing")
	if err == nil || !strings.Contains(err.Error(), "repos.clone") {
		t.Errorf("an unknown repo: %v", err)
	}
}
