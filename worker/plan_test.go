package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
)

// The plan pack (agent/packs/text/plan.md): "The request ... is also the first comment on the `where=\"plan\"`
// thread", and the plan docs are "markdown files at the workspace root, outside the projects. The user comments on
// them line by line". A comment on PLAN.md is where="PLAN.md:14", with no project, in a session of two.
func TestPlanThreadsReachTheAgentByPlace(t *testing.T) {
	r := newRig(t)
	other := gittest.New(t)
	other.Write(map[string]string{"app.ts": "export {}\n"})
	other.Commit("Ada", "first")
	id := r.start(core.SessionSpec{Kind: core.KindPlan, Prompt: "Round invoice lines half-to-even", Provider: "fake",
		Repos: []core.RepoID{core.RepoID(r.repo.Dir), core.RepoID(other.Dir)}})
	a := r.agent()
	first := a.WaitSent(t)
	if !strings.Contains(first.Text, `where="plan"`) {
		t.Errorf("the prompt's thread isn't the plan's:\n%s", first.Text)
	}
	se := r.session(id)
	if !se.StartWork || !se.Planning() {
		t.Fatalf("a plan session doesn't wait on Start work: %+v", se)
	}
	os.WriteFile(filepath.Join(se.Dir, "PLAN.md"), []byte("# Round\n\n## Approach\n\n- Round each line in minor units, ties to even.\n"), 0o644)
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{})
	r.eventually(id, "the turn to end", func(se core.SessionState) bool { return !se.Agent.Working })
	_, err := r.w.Comment(context.Background(), CommentReq{Session: id, Body: "half-up?",
		Anchor: &core.Anchor{Project: core.DocsProject, File: "PLAN.md", Line: 5, Text: "- Round each line in minor units, ties to even."}})
	if err != nil {
		t.Fatal(err)
	}
	got := a.WaitSent(t)
	if !strings.Contains(got.Text, `where="PLAN.md:5">`) || strings.Contains(got.Text, core.DocsProject) {
		t.Errorf("the doc's comment:\n%s", got.Text)
	}
	// The agent's own comment on a doc reads its anchor from the workspace, not a project.
	tid := a.Call(t, "comment", map[string]any{"file": "PLAN.md", "line": 3, "project": core.DocsProject, "body": "Is this the whole approach?"})
	th, _ := r.session(id).Thread(core.ThreadID(tid))
	if th.Anchor == nil || th.Anchor.Text != "## Approach" {
		t.Errorf("the agent's comment on a doc: %+v", th.Anchor)
	}
}

// crit's Start work in a workspace (internal/prompts/defaults/55-plan.startwork.md): "Edit inside these worktrees,
// not their original checkouts:" followed by "one bullet per project — its worktree, the repository it is of, and
// its branch".
func TestStartWorkNamesTheWorktrees(t *testing.T) {
	r := newRig(t)
	id := r.start(core.SessionSpec{Kind: core.KindPlan, Prompt: "Round invoice lines", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	if err := r.w.StartWork(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	se := r.session(id)
	p := se.Projects[0]
	plan, _ := se.Thread(se.PlanThread())
	last, _ := plan.Last()
	for _, want := range []string{"Start work", "PLAN.md", "Edit inside these worktrees, not their original checkouts:",
		"- `" + p.Dir + "`: " + r.repo.Dir + " (branch `" + p.Branch + "`)"} {
		if !strings.Contains(last.Body, want) {
			t.Errorf("Start work lacks %q:\n%s", want, last.Body)
		}
	}
	if se.Planning() {
		t.Error("still planning after Start work")
	}
	if err := r.w.StartWork(context.Background(), id); err == nil {
		t.Error("Start work twice")
	}
	work := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "x", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	if err := r.w.StartWork(context.Background(), work); err == nil {
		t.Error("Start work on a work session")
	}
}
