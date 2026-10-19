package host

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
)

// cloneAt clones src into dir.
func cloneAt(t *testing.T, src, dir string, like *gittest.Repo) *gittest.Repo {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	g := &gittest.Repo{T: t, Dir: dir, When: like.When}
	g.Git("clone", "-q", src, ".")
	return g
}

// cloned is a registered clone of the rig's repo, with the rig's repo as its origin.
func (r *rig) cloned() (core.Repo, *gittest.Repo) {
	r.t.Helper()
	g := cloneAt(r.t, r.repo.Dir, filepath.Join(r.t.TempDir(), "pricing"), r.repo)
	repo, err := r.w.AddRepoPath(context.Background(), g.Dir)
	if err != nil {
		r.t.Fatal(err)
	}
	return repo, g
}

func names(p HousekeepPlan) map[string]string {
	out := map[string]string{}
	for _, b := range p.Branches {
		out[b.Name] = b.Why
	}
	return out
}

// repos-page.md, Housekeeping: merged branches and those whose upstream is gone; never the default or current
// branch; a dirty worktree goes only when ticked.
func TestHousekeeping(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	repo, g := r.cloned()
	// merged: a branch at main.
	g.Git("branch", "done")
	// gone: pushed, then deleted on origin after a squash merge.
	g.Git("checkout", "-q", "-b", "squashed")
	g.Write(map[string]string{"s.go": "package pricing\n"})
	g.Commit("Ada", "squashed work")
	g.Git("push", "-q", "-u", "origin", "squashed")
	r.repo.Git("branch", "-D", "squashed")
	g.Git("checkout", "-q", "main")
	// live: unmerged, kept.
	g.Git("checkout", "-q", "-b", "live")
	g.Write(map[string]string{"l.go": "package pricing\n"})
	g.Commit("Ada", "live work")
	g.Git("checkout", "-q", "main")
	// dirty: merged, in a worktree with changes.
	wt := filepath.Join(t.TempDir(), "dirty")
	g.Git("worktree", "add", "-q", "-b", "dirty", wt)
	os.WriteFile(filepath.Join(wt, "x.txt"), []byte("wip\n"), 0o644)
	// stale: a worktree whose directory went.
	stale := filepath.Join(t.TempDir(), "stale")
	g.Git("worktree", "add", "-q", "-b", "stale-wt", stale)
	os.RemoveAll(stale)

	plan, err := r.w.Housekeeping(ctx, HousekeepReq{Repo: repo.ID, Fetch: true})
	if err != nil {
		t.Fatal(err)
	}
	got := names(plan)
	want := map[string]string{"done": WhyMerged, "squashed": WhyGone, "dirty": WhyMerged, "stale-wt": WhyMerged}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q (plan %v)", k, got[k], v, got)
		}
	}
	if _, ok := got["main"]; ok {
		t.Error("the default branch is offered")
	}
	if _, ok := got["live"]; ok {
		t.Error("an unmerged branch is offered")
	}
	if len(plan.Stale) != 1 {
		t.Errorf("stale = %v", plan.Stale)
	}
	i := slices.IndexFunc(plan.Branches, func(b StaleBranch) bool { return b.Name == "dirty" })
	if !plan.Branches[i].Dirty || plan.Branches[i].Worktree == "" {
		t.Errorf("dirty = %+v", plan.Branches[i])
	}

	res, err := r.w.Housekeep(ctx, HousekeepReq{Repo: repo.ID, Apply: []string{"done", "squashed", "dirty", "live"}, Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	errs := map[string]string{}
	for _, c := range res {
		errs[c.Branch] = c.Error
	}
	if errs["done"] != "" || errs["squashed"] != "" || !strings.Contains(errs["dirty"], "uncommitted") || errs["live"] == "" {
		t.Fatalf("results = %v", errs)
	}
	branches := g.Git("branch", "--format=%(refname:short)")
	for _, b := range []string{"done", "squashed"} {
		if strings.Contains(branches, b+"\n") {
			t.Errorf("%s is still there", b)
		}
	}
	if !strings.Contains(branches, "live\n") || !strings.Contains(branches, "dirty\n") {
		t.Errorf("kept branches went: %s", branches)
	}
	if again, _ := r.w.Housekeeping(ctx, HousekeepReq{Repo: repo.ID}); len(again.Stale) != 0 {
		t.Errorf("stale after prune = %v", again.Stale)
	}
	if res, _ := r.w.Housekeep(ctx, HousekeepReq{Repo: repo.ID, Apply: []string{"dirty"}, Force: []string{"dirty"}}); res[0].Error != "" {
		t.Errorf("forcing a dirty worktree: %v", res)
	}
	if _, err := os.Stat(wt); err == nil {
		t.Error("the dirty worktree is still there")
	}
}

// A branch a live session works on is shown, not cleaned.
func TestHousekeepingKeepsSessionBranches(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	repo, _ := r.cloned()
	id := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "x", Branch: "eng-1", Repos: []core.RepoID{repo.ID}})
	plan, err := r.w.Housekeeping(ctx, HousekeepReq{Repo: repo.ID})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(plan.Branches, func(b StaleBranch) bool { return b.Name == "eng-1" })
	if i < 0 || plan.Branches[i].Session != string(id) {
		t.Fatalf("plan = %+v", plan)
	}
	res, _ := r.w.Housekeep(ctx, HousekeepReq{Repo: repo.ID, Apply: []string{"eng-1"}})
	if len(res) != 1 || !strings.Contains(res[0].Error, "session") {
		t.Errorf("cleaning a session's branch: %v", res)
	}
}

// repos-page.md, Tags: pull first, annotated, only the new tag pushed; a failed push keeps the local tag.
func TestTagPullsAndPushes(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	bare := filepath.Join(t.TempDir(), "origin.git")
	r.repo.Git("clone", "-q", "--bare", r.repo.Dir, bare)
	g := cloneAt(t, bare, filepath.Join(t.TempDir(), "pricing"), r.repo)
	g.Git("tag", "-a", "-m", "v0.4.2", "v0.4.2")
	g.Git("tag", "-a", "-m", "old", "old-local")
	repo, err := r.w.AddRepoPath(ctx, g.Dir)
	if err != nil {
		t.Fatal(err)
	}
	// origin moves on; the clone hasn't pulled it.
	other := cloneAt(t, bare, filepath.Join(t.TempDir(), "other"), r.repo)
	other.Write(map[string]string{"new.go": "package pricing\n"})
	other.Commit("Bob", "the release fix")
	other.Git("push", "-q", "origin", "main")

	p, err := r.w.TagPreview(ctx, repo.ID, "")
	if err != nil || p.Latest != "v0.4.2" || p.Default != "main" || !p.Remote {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	tg, err := r.w.Tag(ctx, TagReq{Repo: repo.ID, Name: "v0.4.3", Message: "v0.4.3", Pull: true, Push: true})
	if err != nil {
		t.Fatal(err)
	}
	if !tg.Pushed || tg.Subject != "the release fix" {
		t.Errorf("tagged %+v", tg)
	}
	remote := (&gittest.Repo{T: t, Dir: bare}).Git("tag")
	if !strings.Contains(remote, "v0.4.3") || strings.Contains(remote, "old-local") {
		t.Errorf("origin's tags = %q: want the new one only", remote)
	}
	if typ := strings.TrimSpace(g.Git("cat-file", "-t", "v0.4.3")); typ != "tag" {
		t.Errorf("not annotated: %s", typ)
	}
	if _, err := r.w.Tag(ctx, TagReq{Repo: repo.ID, Name: "v0.4.3", Message: "again"}); err == nil {
		t.Error("tagging an existing name")
	}
	g.Git("remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	tg, err = r.w.Tag(ctx, TagReq{Repo: repo.ID, Name: "v0.4.4", Message: "v0.4.4", Push: true})
	if err != nil || tg.Pushed || tg.PushErr == "" {
		t.Fatalf("a failed push: %+v, %v", tg, err)
	}
	if !strings.Contains(g.Git("tag"), "v0.4.4") {
		t.Error("a failed push dropped the local tag")
	}
}

// Pull before tag refuses a dirty checked-out branch rather than stash.
func TestTagRefusesDirtyPull(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	repo, g := r.cloned()
	os.WriteFile(filepath.Join(g.Dir, "round.go"), []byte("changed\n"), 0o644)
	if _, err := r.w.Tag(ctx, TagReq{Repo: repo.ID, Name: "v1.0.0", Message: "v1", Pull: true}); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("tag over a dirty branch: %v", err)
	}
	if strings.Contains(g.Git("tag"), "v1.0.0") {
		t.Error("tagged anyway")
	}
}

func TestRepoFacts(t *testing.T) {
	r := newRig(t)
	repo, g := r.cloned()
	g.Git("tag", "-a", "-m", "v1.2.3", "v1.2.3")
	g.Git("branch", "other")
	f, err := r.w.RepoFacts(context.Background(), repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.Branch != "main" || f.Branches != 2 || f.Tags != 1 || f.LatestTag != "v1.2.3" || f.Last.Subject != "first" || f.SizeKiB == 0 {
		t.Errorf("facts = %+v", f)
	}
}
