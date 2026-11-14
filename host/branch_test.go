package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
)

// branchSession is a work session on a checkout whose origin is a bare repo, with its agent started: the project,
// its worktree as a repo, and the agent.
func branchSession(t *testing.T, r *rig) (core.SessionID, core.Project, *gittest.Repo, *agenttest.FakeSession) {
	t.Helper()
	id, p, wt, a, _ := branchSessionUp(t, r)
	return id, p, wt, a
}

// branchSessionUp is branchSession with another clone of origin, to move main on.
func branchSessionUp(t *testing.T, r *rig) (core.SessionID, core.Project, *gittest.Repo, *agenttest.FakeSession, *gittest.Repo) {
	t.Helper()
	api, up := githubRepo(t, "api", map[string]string{"limiter.go": "package limiter\n"})
	id := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Back off on 429", Provider: "fake", Repos: []core.RepoID{core.RepoID(api.Dir)}})
	a := r.agent()
	p := r.session(id).Projects[0]
	return id, p, &gittest.Repo{T: t, Dir: p.Dir, When: api.When}, a, up
}

func (r *rig) project(id core.SessionID) core.Project {
	r.t.Helper()
	return r.session(id).Projects[0]
}

func notesOf(t *testing.T, se core.SessionState) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(se.Dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The notes say which branch each project is on, and to change branches through Oriel's tools, never git.
func TestNotesNameTheBranchAndTheTools(t *testing.T) {
	r := newRig(t)
	id, p, _, _ := branchSession(t, r)
	notes := notesOf(t, r.session(id))
	for _, want := range []string{"on branch `" + p.Branch + "`", "Don't create, switch or rename branches with git",
		"`mcp__oriel__new_branch`", "`mcp__oriel__rename_branch`"} {
		if !strings.Contains(notes, want) {
			t.Errorf("the notes haven't %q:\n%s", want, notes)
		}
	}
}

// rename_branch renames the session's branch, records it, and the notes and a push follow it.
func TestRenameBranchTool(t *testing.T) {
	r := newRig(t)
	id, p, wt, a := branchSession(t, r)
	out := a.Call(t, "rename_branch", map[string]any{"name": "tester/backoff"})
	if !strings.Contains(out, "tester/backoff") {
		t.Errorf("the answer: %q", out)
	}
	if got := r.project(id).Branch; got != "tester/backoff" {
		t.Fatalf("the session's branch is %q", got)
	}
	if got := branchOf(t, p.Dir); got != "tester/backoff" {
		t.Errorf("the worktree is on %q", got)
	}
	if strings.TrimSpace(wt.Git("branch", "--list", p.Branch)) != "" {
		t.Errorf("the old branch %s is still there", p.Branch)
	}
	if notes := notesOf(t, r.session(id)); !strings.Contains(notes, "on branch `tester/backoff`") {
		t.Errorf("the notes don't follow the rename:\n%s", notes)
	}
	if err := r.w.ShipPush(context.Background(), ShipRef{Session: id, Project: p.Name}); err != nil {
		t.Fatal(err)
	}
	if out := wt.Git("ls-remote", "origin", "tester/backoff"); !strings.Contains(out, "tester/backoff") {
		t.Errorf("origin hasn't the renamed branch: %q", out)
	}
	if _, err := a.CallErr("rename_branch", map[string]any{"name": "main"}); err == nil {
		t.Error("renamed onto the default branch's name")
	}
}

// rename_branch keeps the name while a code host last said the branch's PR is open: renaming would leave it behind.
func TestRenameBranchWaitsForAnOpenPR(t *testing.T) {
	r := newRig(t)
	id, p, _, a := branchSession(t, r)
	r.w.SawPR(PRSeenReq{Session: id, Project: p.Name, Branch: p.Branch, Open: true})
	if _, err := a.CallErr("rename_branch", map[string]any{"name": "tester/backoff"}); err == nil || !strings.Contains(err.Error(), "is open") {
		t.Fatalf("renamed under an open PR: %v", err)
	}
	r.w.SawPR(PRSeenReq{Session: id, Project: p.Name, Branch: p.Branch})
	a.Call(t, "rename_branch", map[string]any{"name": "tester/backoff"})
	if got := r.project(id).Branch; got != "tester/backoff" {
		t.Errorf("once the PR closed, the branch is %q", got)
	}
}

// new_branch starts a fresh branch off the default branch, freshly fetched, and the session takes it; the old one is
// kept locally, and the answer says so.
func TestNewBranchTool(t *testing.T) {
	r := newRig(t)
	id, p, wt, a, up := branchSessionUp(t, r)
	wt.Write(map[string]string{"retry.go": "package limiter\n"})
	wt.Commit("Ada", "retry")
	up.Write(map[string]string{"README": "docs\n"})
	up.Commit("Bo", "docs")
	up.Git("push", "-q", "origin", "main")

	out := a.Call(t, "new_branch", map[string]any{"name": "tester/second"})
	if !strings.Contains(out, "tester/second") || !strings.Contains(out, p.Branch) || !strings.Contains(out, "kept") {
		t.Errorf("the answer doesn't name both branches and say the old one is kept: %q", out)
	}
	if got := r.project(id); got.Branch != "tester/second" || got.Base != "main" {
		t.Fatalf("the session's project: %+v", got)
	}
	if got := branchOf(t, p.Dir); got != "tester/second" {
		t.Errorf("the worktree is on %q", got)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "retry.go")); err == nil {
		t.Error("the new branch has the old one's commit")
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "README")); err != nil {
		t.Error("the new branch isn't cut from origin's main as it is now")
	}
	if strings.TrimSpace(wt.Git("branch", "--list", p.Branch)) == "" {
		t.Errorf("the old branch %s is gone", p.Branch)
	}
	if notes := notesOf(t, r.session(id)); !strings.Contains(notes, "on branch `tester/second`") {
		t.Errorf("the notes don't follow the new branch:\n%s", notes)
	}
	if out := a.Call(t, "new_branch", map[string]any{}); !strings.Contains(out, "Switched") {
		t.Errorf("with no name: %q", out)
	}
	if b := r.project(id).Branch; b == "tester/second" || b == "" {
		t.Errorf("with no name, the branch is %q", b)
	}
}

// Uncommitted work stops new_branch, unless carry says to take it along.
func TestNewBranchRefusesUncommittedWork(t *testing.T) {
	r := newRig(t)
	id, p, _, a := branchSession(t, r)
	if err := os.WriteFile(filepath.Join(p.Dir, "wip.go"), []byte("package limiter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CallErr("new_branch", map[string]any{"name": "tester/second"}); err == nil || !strings.Contains(err.Error(), "uncommitted") ||
		!strings.Contains(err.Error(), "carry") {
		t.Fatalf("with uncommitted work: %v", err)
	}
	if got := r.project(id).Branch; got != p.Branch {
		t.Fatalf("the branch moved to %q", got)
	}
	a.Call(t, "new_branch", map[string]any{"name": "tester/second", "carry": true})
	if got := branchOf(t, p.Dir); got != "tester/second" {
		t.Errorf("the worktree is on %q", got)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "wip.go")); err != nil {
		t.Error("the uncommitted work didn't come along")
	}
}

// On a branch of the agent's own, a push is refused and says how to get out of it; the Ship read shows it, and
// adopting it, once it holds the session's work, makes it the session's branch: then it pushes.
func TestAdoptingABranchMakesItPushable(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	id, p, wt, _ := branchSession(t, r)
	wt.Write(map[string]string{"retry.go": "package limiter\n"})
	wt.Commit("Ada", "retry")
	wt.Git("checkout", "-q", "-b", "agent/own")
	wt.Write(map[string]string{"more.go": "package limiter\n"})
	wt.Commit("Ada", "more")

	err := r.w.ShipPush(ctx, ShipRef{Session: id, Project: p.Name})
	if err == nil || !strings.Contains(err.Error(), "git checkout "+p.Branch) || !strings.Contains(err.Error(), "new_branch / rename_branch") {
		t.Fatalf("pushing the agent's branch: %v", err)
	}
	off := r.ship(id, false)[p.Name].Off
	if off == nil || off.Session != p.Branch || off.Current != "agent/own" || off.Refused != "" {
		t.Fatalf("the read: %+v", off)
	}
	if err := r.w.ShipAdopt(ctx, ShipRef{Session: id, Project: p.Name}); err != nil {
		t.Fatal(err)
	}
	if got := r.project(id).Branch; got != "agent/own" {
		t.Fatalf("the session's branch is %q", got)
	}
	if off := r.ship(id, false)[p.Name].Off; off != nil {
		t.Errorf("still off: %+v", off)
	}
	m, _ := LoadManifest(r.session(id).Dir)
	if m.Projects[0].Branch != "agent/own" {
		t.Errorf("the manifest has %q", m.Projects[0].Branch)
	}
	if err := r.w.ShipPush(ctx, ShipRef{Session: id, Project: p.Name}); err != nil {
		t.Fatal(err)
	}
	if out := wt.Git("ls-remote", "origin", "agent/own"); !strings.Contains(out, "agent/own") {
		t.Errorf("origin hasn't it: %q", out)
	}
}

// Switching back puts the worktree on the session's branch again.
func TestSwitchBackToTheSessionsBranch(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	id, p, wt, _ := branchSession(t, r)
	wt.Git("checkout", "-q", "-b", "agent/own")
	if err := r.w.ShipSwitchBack(ctx, ShipRef{Session: id, Project: p.Name}); err != nil {
		t.Fatal(err)
	}
	if got := branchOf(t, p.Dir); got != p.Branch {
		t.Errorf("the worktree is on %q", got)
	}
	if off := r.ship(id, false)[p.Name].Off; off != nil {
		t.Errorf("still off: %+v", off)
	}
	if err := r.w.ShipSwitchBack(ctx, ShipRef{Session: id, Project: p.Name}); err == nil {
		t.Error("switched back while on the branch")
	}
}

// What can't be adopted says why, and isn't: the default branch, another session's, a detached HEAD, a branch that
// doesn't hold the session's commits, and one origin has already. Each push stays refused.
func TestAdoptRefusals(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	id, p, wt, _ := branchSession(t, r)
	wt.Write(map[string]string{"retry.go": "package limiter\n"})
	wt.Commit("Ada", "retry")
	other := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Other", Provider: "fake", Repos: []core.RepoID{core.RepoID(p.Repo)}})
	op := r.project(other)
	(&gittest.Repo{T: t, Dir: op.Dir}).Git("checkout", "-q", "--detach")
	main := &gittest.Repo{T: t, Dir: p.Repo}
	main.Git("checkout", "-q", "--detach")
	main.Git("branch", "-q", "taken", "origin/main")
	main.Git("push", "-q", "origin", "taken")
	main.Git("branch", "-q", "-D", "taken")
	main.Git("fetch", "-q", "origin")

	for _, c := range []struct {
		name, checkout, why string
	}{
		{"the default branch", "main", "default branch"},
		{"another session's branch", op.Branch, "another session's"},
		{"a detached HEAD", "--detach", "detached"},
		{"a branch cut fresh from main", "-b fresh origin/main", "doesn't hold"},
		{"a branch origin has", "-b taken", "origin has"},
	} {
		t.Run(c.name, func(t *testing.T) {
			wt.Git("checkout", "-q", p.Branch)
			wt.Git(append([]string{"checkout", "-q"}, strings.Fields(c.checkout)...)...)
			off := r.ship(id, false)[p.Name].Off
			if off == nil || !strings.Contains(off.Refused, c.why) {
				t.Fatalf("the read: %+v", off)
			}
			if err := r.w.ShipAdopt(ctx, ShipRef{Session: id, Project: p.Name}); err == nil || !strings.Contains(err.Error(), c.why) {
				t.Errorf("adopting: %v", err)
			}
			if got := r.project(id).Branch; got != p.Branch {
				t.Errorf("the session's branch moved to %q", got)
			}
			if err := r.w.ShipPush(ctx, ShipRef{Session: id, Project: p.Name}); err == nil {
				t.Error("pushed")
			}
		})
	}
}
