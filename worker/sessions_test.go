package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/gitx/gittest"
)

func branchOf(t *testing.T, dir string) string {
	t.Helper()
	b, err := gitx.Repo{Dir: dir}.Branch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// AI-1 done when: crit's workspace layout is read in place.
func TestCritWorkspacesAreReadInPlace(t *testing.T) {
	r := newRig(t)
	crit := t.TempDir()
	root := filepath.Join(crit, "work", "p5")
	wt := filepath.Join(root, "repos", "pricing")
	ctx := context.Background()
	if err := (gitx.Repo{Dir: r.repo.Dir}).AddWorktree(ctx, wt, "p5", "main"); err != nil {
		t.Fatal(err)
	}
	m := map[string]any{"name": "p5", "createdAt": "2026-10-14T07:26:37Z", "kind": "work",
		"projects": []map[string]any{{"name": "pricing", "repo": r.repo.Dir, "dir": "repos/pricing", "branch": "p5", "primary": true}}}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(root, CritManifestFile), b, 0o644); err != nil {
		t.Fatal(err)
	}
	r.w.opts.CritHome = crit
	r.w.readCrit()
	se := r.session("crit-p5")
	if se.Source != "crit" || se.Kind != core.KindWork || se.Dir != root || se.Branch != "p5" {
		t.Fatalf("session = %+v", se)
	}
	if len(se.Projects) != 1 || se.Projects[0].Dir != wt || se.Projects[0].Repo != r.repo.Dir || !se.Projects[0].Primary {
		t.Fatalf("projects = %+v", se.Projects)
	}
	// In place: nothing was copied or written into crit's workspace.
	if _, err := os.Stat(filepath.Join(root, ManifestFile)); err == nil {
		t.Error("Oriel wrote its own manifest into crit's workspace")
	}
	// Oriel's additions survive a restart, with the layout still crit's.
	if _, err := r.w.Comment(ctx, CommentReq{Session: "crit-p5", Body: "hello from oriel"}); err != nil {
		t.Fatal(err)
	}
	r.restart()
	r.w.opts.CritHome = crit
	r.w.readCrit()
	if se := r.session("crit-p5"); len(se.Threads) != 1 || se.Projects[0].Dir != wt {
		t.Fatalf("after a restart: %+v", se)
	}
}

// SE-1: each kind starts as sessions.md says.
func TestWorkSessionGetsAWorktreeOnAFreshBranch(t *testing.T) {
	r := newRig(t)
	id := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Fix stale usage prices", Ticket: "INF-2231", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	se := r.session(id)
	if id != "inf-2231-fix-stale-usage-prices" {
		t.Errorf("id = %q", id)
	}
	if se.Dir != filepath.Join(r.home, "workspaces", string(id)) {
		t.Errorf("dir = %q", se.Dir)
	}
	p, _ := se.Primary()
	if want := filepath.Join(se.Dir, "repos", filepath.Base(r.repo.Dir)); p.Dir != want {
		t.Errorf("project dir = %q, want %q", p.Dir, want)
	}
	if got := branchOf(t, p.Dir); got != "tester/inf-2231-fix-stale-usage-prices" || se.Branch != got {
		t.Errorf("branch = %q (state %q)", got, se.Branch)
	}
	if !gitx.IsRepo(se.Dir) {
		t.Error("the workspace isn't a git repo of its own, for plan docs")
	}
	if m, src, ok := LoadManifest(se.Dir); !ok || src != "oriel" || len(m.Projects) != 1 {
		t.Errorf("manifest = %+v %q %v", m, src, ok)
	}
	// The prompt opened the first request.
	if len(se.Threads) != 1 || se.Threads[0].Title != "Fix stale usage prices" || se.Threads[0].Anchor != nil {
		t.Errorf("threads = %+v", se.Threads)
	}
	if se.Unplanned {
		t.Error("a work session from a ticket isn't unplanned")
	}
}

func TestWorkNeedsARepo(t *testing.T) {
	r := newRig(t)
	if _, err := r.w.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Prompt: "x"}); err == nil {
		t.Fatal("a work session started with no repo")
	}
}

func TestDirectNeverCreatesAWorktree(t *testing.T) {
	r := newRig(t)
	before := (gitx.Repo{Dir: r.repo.Dir})
	id := r.start(core.SessionSpec{Kind: core.KindDirect, Dir: r.repo.Dir})
	se := r.session(id)
	if se.Dir != r.repo.Dir || len(se.Projects) != 1 || se.Projects[0].Dir != r.repo.Dir || se.Projects[0].Repo != "" {
		t.Fatalf("session = %+v", se)
	}
	out, _ := before.Run(context.Background(), "worktree", "list")
	if n := strings.Count(strings.TrimSpace(out), "\n"); n != 0 {
		t.Errorf("a direct session made a worktree:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(r.home, "workspaces", string(id))); err == nil {
		t.Error("a direct session made a workspace")
	}
	if got := branchOf(t, r.repo.Dir); got != "main" {
		t.Errorf("the checkout moved to %q", got)
	}
	// A directory that isn't a repo opens directly too.
	plain := t.TempDir()
	id = r.start(core.SessionSpec{Kind: core.KindDirect, Dir: plain})
	if se := r.session(id); se.Dir != plain || se.Branch != "" {
		t.Errorf("plain directory: %+v", se)
	}
}

func TestPlanWorktreesComeAtStartWork(t *testing.T) {
	r := newRig(t)
	id := r.start(core.SessionSpec{Kind: core.KindPlan, Prompt: "Round invoice lines", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	se := r.session(id)
	if p := se.Projects[0]; p.Dir != r.repo.Dir || !p.ReadOnly {
		t.Fatalf("before Start work the plan reads the checkout: %+v", p)
	}
	if _, err := os.Stat(filepath.Join(se.Dir, "repos")); err == nil {
		t.Error("a plan made worktrees before Start work")
	}
	// The plan's own thread doesn't hold up Start work; another open one does.
	tid, _ := r.w.Comment(context.Background(), CommentReq{Session: id, Anchor: &core.Anchor{File: "PLAN.md", Line: 3}, Body: "half-up?"})
	if err := r.w.StartWork(context.Background(), id); err == nil {
		t.Fatal("Start work with a thread open")
	}
	if err := r.w.SetThread(ThreadReq{Session: id, Thread: tid, Status: core.ThreadDeferred}); err != nil {
		t.Fatal(err)
	}
	if err := r.w.StartWork(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	se = r.session(id)
	p := se.Projects[0]
	if p.ReadOnly || p.Dir != filepath.Join(se.Dir, "repos", filepath.Base(r.repo.Dir)) || !se.Started {
		t.Fatalf("after Start work: %+v", se)
	}
	if got := branchOf(t, p.Dir); !strings.HasPrefix(got, "tester/round-invoice-lines") {
		t.Errorf("branch = %q", got)
	}
	plan, _ := se.Thread(se.PlanThread())
	if last, _ := plan.Last(); !strings.HasPrefix(last.Body, "**Start work**") {
		t.Errorf("the agent wasn't told to start: %+v", plan)
	}
}

func TestTourIsPinnedAndReadOnly(t *testing.T) {
	r := newRig(t)
	id := r.start(core.SessionSpec{Kind: core.KindTour, Prompt: "How does rounding work?", TourKind: "walkthrough", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	p := r.session(id).Projects[0]
	if !p.Detached || !p.ReadOnly || p.Commit == "" || branchOf(t, p.Dir) != "HEAD" {
		t.Fatalf("tour project = %+v", p)
	}
	m, _, _ := LoadManifest(r.session(id).Dir)
	if m.Tour == nil || m.Tour.Kind != "walkthrough" {
		t.Errorf("manifest tour = %+v", m.Tour)
	}
}

func TestReviewChecksOutThePRHead(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	// An origin whose pull/7/head is a commit main doesn't have.
	origin := r.repo
	origin.Git("checkout", "-q", "-b", "feature")
	origin.Write(map[string]string{"round.go": "package pricing\n\nfunc Round() { /* pr */ }\n"})
	origin.Commit("Bob", "the PR")
	head := strings.TrimSpace(origin.Git("rev-parse", "HEAD"))
	origin.Git("update-ref", "refs/pull/7/head", head)
	origin.Git("checkout", "-q", "main")
	clone := &gittest.Repo{T: t, Dir: t.TempDir(), When: origin.When}
	clone.Git("clone", "-q", origin.Dir, ".")
	clone.Git("remote", "set-url", "origin", origin.Dir)
	if _, err := r.w.repos.add(ctx, r.w.opts.ID, clone.Dir); err != nil {
		t.Fatal(err)
	}
	id := r.start(core.SessionSpec{Kind: core.KindReview, PR: "https://github.com/acme/pricing/pull/7", Repos: []core.RepoID{core.RepoID(clone.Dir)}})
	se := r.session(id)
	p := se.Projects[0]
	if !p.ReadOnly || !p.Detached || p.Commit != head || se.PR == "" {
		t.Fatalf("review project = %+v, want detached at %s", p, head)
	}
}

func TestNewRepoInitsAndRegisters(t *testing.T) {
	r := newRig(t)
	parent := t.TempDir()
	id := r.start(core.SessionSpec{Kind: core.KindNewRepo, Name: "invoicer", Dir: parent, Template: "go-cli", Prompt: "what are we building?"})
	dir := filepath.Join(parent, "invoicer")
	if !gitx.IsRepo(dir) {
		t.Fatal("no repo was made")
	}
	if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err != nil || !strings.Contains(string(b), "module github.com/tester/invoicer") {
		t.Errorf("go.mod = %q, %v", b, err)
	}
	if out := (&gittest.Repo{T: t, Dir: dir}).Git("log", "--oneline"); strings.Count(out, "\n") != 1 {
		t.Errorf("log = %q, want one commit", out)
	}
	if _, ok := r.w.repos.find(dir); !ok {
		t.Error("the new repo isn't registered")
	}
	se := r.session(id)
	if se.Kind != core.KindNewRepo || len(se.Projects) != 1 || se.Projects[0].Repo != dir {
		t.Errorf("session = %+v", se)
	}
	if _, err := r.w.Start(context.Background(), core.SessionSpec{Kind: core.KindNewRepo, Name: "invoicer", Dir: parent}); err == nil {
		t.Error("a second repo of the same name was made over the first")
	}
}

func TestScratchHasNoRepo(t *testing.T) {
	r := newRig(t)
	id := r.start(core.SessionSpec{Kind: core.KindScratch})
	se := r.session(id)
	if len(se.Projects) != 0 || se.Dir == "" {
		t.Fatalf("scratch = %+v", se)
	}
	if fi, err := os.Stat(se.Dir); err != nil || !fi.IsDir() {
		t.Error("scratch has nowhere to keep notes")
	}
}

func TestPluginKinds(t *testing.T) {
	r := newRig(t)
	off := r.w.Kinds().Register(KindSpec{Kind: "incident", Title: "Incident", Pack: "work", Worktrees: WorktreesNow, NeedsRepo: true})
	id := r.start(core.SessionSpec{Kind: "incident", Prompt: "pager went off", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	if se := r.session(id); se.Kind != "incident" || len(se.Projects) != 1 {
		t.Errorf("incident session = %+v", se)
	}
	off()
	if _, err := r.w.Start(context.Background(), core.SessionSpec{Kind: "incident", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}}); err == nil {
		t.Error("a removed kind still starts")
	}
}

// SE-2: archived sessions keep threads and transcripts searchable; restart restores them.
func TestArchiveKeepsThreadsAndTranscriptSearchable(t *testing.T) {
	r := newRig(t)
	id := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Use banker's rounding for JPY", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	_ = r.w.emit(core.TranscriptAppended{Session: id, Items: []core.Item{{ID: "i1", Kind: core.ItemAssistant, Text: "Floor rounds toward minus infinity"}}})
	wt := r.session(id).Projects[0].Dir
	if err := os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.w.Archive(context.Background(), id); err == nil {
		t.Fatal("archived over uncommitted work")
	}
	os.Remove(filepath.Join(wt, "dirty.txt"))
	if err := r.w.Archive(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); err == nil {
		t.Error("the worktree is still there")
	}
	r.restart()
	se := r.session(id)
	if !se.Archived || se.Status != core.StatusOff || len(se.Threads) != 1 || len(se.Transcript) != 1 {
		t.Fatalf("archived session after a restart: %+v", se)
	}
	if hits := r.w.Search(SearchReq{Query: "banker"}); len(hits) != 0 {
		t.Errorf("archived sessions show without asking: %+v", hits)
	}
	hits := r.w.Search(SearchReq{Query: "BANKER", Archived: true})
	if len(hits) != 1 || hits[0].Thread == "" {
		t.Errorf("thread hits = %+v", hits)
	}
	hits = r.w.Search(SearchReq{Query: "minus infinity", Archived: true})
	if len(hits) != 1 || hits[0].Item != "i1" {
		t.Errorf("transcript hits = %+v", hits)
	}
}

func TestNextPRCutsAFreshBranchAndKeepsThePast(t *testing.T) {
	r := newRig(t)
	id := r.start(core.SessionSpec{Kind: core.KindWork, Name: "rounding", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	first := r.session(id).Branch
	if err := r.w.NextPR(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	se := r.session(id)
	if se.Branch == first || branchOf(t, se.Projects[0].Dir) != se.Branch {
		t.Errorf("branch %q → %q", first, se.Branch)
	}
	if len(se.Past) != 1 || se.Past[0].Branch != first {
		t.Errorf("past = %+v", se.Past)
	}
	m, _, _ := LoadManifest(se.Dir)
	if len(m.Projects[0].Past) != 1 || m.Projects[0].Branch != se.Branch {
		t.Errorf("manifest = %+v", m.Projects[0])
	}
}

func TestAddRepoMidSession(t *testing.T) {
	r := newRig(t)
	other := gittest.New(t)
	other.Write(map[string]string{"a": "a\n"})
	other.Commit("Ada", "a")
	id := r.start(core.SessionSpec{Kind: core.KindWork, Name: "both", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	if err := r.w.AddRepo(context.Background(), id, core.RepoID(other.Dir)); err != nil {
		t.Fatal(err)
	}
	se := r.session(id)
	if len(se.Projects) != 2 || branchOf(t, se.Projects[1].Dir) != se.Branch {
		t.Fatalf("projects = %+v, branch %q", se.Projects, se.Branch)
	}
	if err := r.w.AddRepo(context.Background(), id, core.RepoID(other.Dir)); err == nil {
		t.Error("the same repo was added twice")
	}
}
