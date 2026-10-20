package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
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
	if err := r.w.StartWork(context.Background(), StartWorkReq{Session: id}); err != nil {
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
	if err := r.w.StartWork(context.Background(), StartWorkReq{Session: id}); err == nil {
		t.Error("Start work twice")
	}
	work := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "x", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	if err := r.w.StartWork(context.Background(), StartWorkReq{Session: work}); err == nil {
		t.Error("Start work on a work session")
	}
}

// planWithAgent is a plan on the rig's repo with the fake agent, its prompt delivered and its turn over.
func planWithAgent(t *testing.T, r *rig) (core.SessionID, *agenttest.FakeSession) {
	t.Helper()
	id := r.start(core.SessionSpec{Kind: core.KindPlan, Prompt: "Round invoice lines", Provider: "fake", Model: "m1", Effort: "low",
		Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	a := r.agent()
	a.WaitSent(t)
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{})
	r.eventually(id, "the agent's first turn", func(se core.SessionState) bool { return !se.Agent.Working && se.Agent.Session != "" })
	return id, a
}

// thread opens a thread on PLAN.md and gives it status.
func (r *rig) thread(id core.SessionID, line int, body string, status core.ThreadStatus) core.ThreadID {
	r.t.Helper()
	tid, err := r.w.Comment(context.Background(), CommentReq{Session: id, Body: body,
		Anchor: &core.Anchor{Project: core.DocsProject, File: "PLAN.md", Line: line}})
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.w.SetThread(ThreadReq{Session: id, Thread: tid, Status: status}); err != nil {
		r.t.Fatal(err)
	}
	return tid
}

// sessions.md, Plan → Start work: "Clear the plan's threads", on by default: the resolved ones go, and the deferred
// ones go too but are carried into the work prompt as a note, so nothing asked is lost. Kept, they stay as they are.
func TestStartWorkClearsThePlansThreads(t *testing.T) {
	r := newRig(t)
	id := r.start(core.SessionSpec{Kind: core.KindPlan, Prompt: "Round invoice lines", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	r.thread(id, 3, "Where does precision come from?", core.ThreadResolved)
	r.thread(id, 9, "Should historical invoices change?", core.ThreadDeferred)
	if err := r.w.StartWork(context.Background(), StartWorkReq{Session: id}); err != nil {
		t.Fatal(err)
	}
	se := r.session(id)
	if len(se.Threads) != 1 || se.Threads[0].ID != se.PlanThread() {
		t.Fatalf("after Start work the threads are %+v", se.Threads)
	}
	plan, _ := se.Thread(se.PlanThread())
	last, _ := plan.Last()
	if !strings.Contains(last.Body, "`PLAN.md:9` (deferred): Should historical invoices change?") || strings.Contains(last.Body, "precision") {
		t.Errorf("the work prompt carries:\n%s", last.Body)
	}

	kept := r.start(core.SessionSpec{Kind: core.KindPlan, Prompt: "Round invoice totals", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	r.thread(kept, 3, "Where does precision come from?", core.ThreadResolved)
	r.thread(kept, 9, "Should historical invoices change?", core.ThreadDeferred)
	if err := r.w.StartWork(context.Background(), StartWorkReq{Session: kept, KeepThreads: true}); err != nil {
		t.Fatal(err)
	}
	se = r.session(kept)
	plan, _ = se.Thread(se.PlanThread())
	last, _ = plan.Last()
	if len(se.Threads) != 3 || strings.Contains(last.Body, "Should historical") {
		t.Errorf("kept threads: %d, and the prompt:\n%s", len(se.Threads), last.Body)
	}
}

// "Restart the agent with a fresh context", on by default: the plan's agent stops, and a new one starts (not a
// resume) with the work pack, told to read the plan docs. Kept, the same agent is told on the plan's thread.
func TestStartWorkRestartsTheAgentFresh(t *testing.T) {
	r := newRig(t)
	id, a := planWithAgent(t, r)
	if err := r.w.StartWork(context.Background(), StartWorkReq{Session: id}); err != nil {
		t.Fatal(err)
	}
	fresh := r.agent()
	if !a.Closed() {
		t.Error("the plan's agent is still running")
	}
	if fresh.Spec.Pack.Name != "work" || len(r.fake.Resumed()) != 0 {
		t.Errorf("the new agent runs pack %q, resumed %v", fresh.Spec.Pack.Name, r.fake.Resumed())
	}
	if in := fresh.WaitSent(t); !strings.Contains(in.Text, "Start work") || !strings.Contains(in.Text, "PLAN.md") {
		t.Errorf("the fresh agent was told %q", in.Text)
	}
	r.eventually(id, "the fresh agent's id", func(se core.SessionState) bool { return se.Agent.Session == fresh.ID() && se.Agent.Pack == "work" })

	keep, b := planWithAgent(t, r)
	if err := r.w.StartWork(context.Background(), StartWorkReq{Session: keep, KeepConversation: true}); err != nil {
		t.Fatal(err)
	}
	if in := b.WaitSent(t); !strings.Contains(in.Text, "Start work") {
		t.Errorf("the plan's agent was told %q", in.Text)
	}
	if b.Closed() || r.session(keep).Agent.Pack != "" {
		t.Error("keeping the conversation restarted the agent")
	}
}

// The work pack (agent/packs/text/work.md): "The request the session started from follows these instructions ...
// Start on it straight away ... With no request, end your turn." A plugin's prompt is "a turn of its own, once the
// session is idle" (Claude Code's $.prompt.submit), so a request sent after the start waits out a turn of the bare
// skill: the fresh agent's first turn carries it (StartSpec.Prompt), and nothing follows it.
func TestStartWorkPromptIsTheFreshAgentsFirstTurn(t *testing.T) {
	r := newRig(t)
	id := r.start(core.SessionSpec{Kind: core.KindPlan, Prompt: "Round invoice lines", Provider: "fake", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	a := r.agent()
	if !strings.Contains(a.Spec.Prompt, "Round invoice lines") {
		t.Errorf("the plan agent's first turn = %q, want its request", a.Spec.Prompt)
	}
	a.WaitSent(t)
	a.Emit(agent.TurnStart{})
	a.Emit(agent.TurnEnd{})
	r.eventually(id, "the agent's first turn", func(se core.SessionState) bool { return !se.Agent.Working && se.Agent.Session != "" })
	if err := r.w.StartWork(context.Background(), StartWorkReq{Session: id}); err != nil {
		t.Fatal(err)
	}
	fresh := r.agent()
	if !strings.Contains(fresh.Spec.Prompt, "**Start work**") || !strings.Contains(fresh.Spec.Prompt, `where="plan"`) {
		t.Fatalf("the fresh agent's first turn = %q, want the Start work prompt on the plan's thread", fresh.Spec.Prompt)
	}
	fresh.WaitSent(t)
	fresh.NoneSent(t)
	r.eventually(id, "the plan's thread given to the agent", func(se core.SessionState) bool {
		th, ok := se.Thread(se.PlanThread())
		return ok && th.Busy && len(th.Undelivered()) == 0
	})
}

// The work phase's model and effort: the agent comes back on them, carrying on its conversation when that's kept.
func TestStartWorkChangesTheModel(t *testing.T) {
	r := newRig(t)
	id, a := planWithAgent(t, r)
	if err := r.w.StartWork(context.Background(), StartWorkReq{Session: id, KeepConversation: true, Model: "m2", Effort: "high"}); err != nil {
		t.Fatal(err)
	}
	b := r.agent()
	if !a.Closed() || b.Spec.Model != "m2" || b.Spec.Effort != "high" || b.Spec.Pack.Name != "plan" {
		t.Errorf("the agent came back on %q %q with pack %q", b.Spec.Model, b.Spec.Effort, b.Spec.Pack.Name)
	}
	if got := r.fake.Resumed(); len(got) != 1 || got[0] != a.ID() {
		t.Errorf("resumed %v, want %s", got, a.ID())
	}
	if se := r.session(id); se.Agent.Model != "m2" || se.Agent.Effort != "high" {
		t.Errorf("the session's agent: %+v", se.Agent)
	}
}

// "Branch name per project, pre-filled from the ticket and prompt": a name given for a project is its branch; one
// git wouldn't take, or for a project that isn't there, refuses before any worktree is made.
func TestStartWorkBranchNames(t *testing.T) {
	r := newRig(t)
	id := r.start(core.SessionSpec{Kind: core.KindPlan, Ticket: "ENG-2231", Prompt: "Round invoice lines", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	name := r.session(id).Projects[0].Name
	if got := r.session(id).Projects[0].Branch; got != "tester/eng-2231-round-invoice-lines" {
		t.Errorf("the branch Start work offers is %q", got)
	}
	for _, bad := range []map[string]string{{name: "two..dots"}, {"nope": "tester/x"}} {
		if err := r.w.StartWork(context.Background(), StartWorkReq{Session: id, Branches: bad}); err == nil {
			t.Errorf("Start work took branches %v", bad)
		}
	}
	if se := r.session(id); se.Started || se.Projects[0].Dir != r.repo.Dir {
		t.Fatalf("a refused Start work moved the session: %+v", se.Projects)
	}
	if err := r.w.StartWork(context.Background(), StartWorkReq{Session: id, Branches: map[string]string{name: "liam/rounding"}}); err != nil {
		t.Fatal(err)
	}
	se := r.session(id)
	if p := se.Projects[0]; p.Branch != "liam/rounding" || branchOf(t, p.Dir) != "liam/rounding" || se.Branch != "liam/rounding" {
		t.Errorf("after Start work: %+v", p)
	}
}

// The work phase's sandbox and network: a host that can't run one refuses; one that can starts the agent in it.
func TestStartWorkSandboxAndNetwork(t *testing.T) {
	r := newRig(t)
	id, _ := planWithAgent(t, r)
	on := true
	if err := r.w.StartWork(context.Background(), StartWorkReq{Session: id, Sandbox: &on}); err == nil {
		t.Error("a host with no sandbox started work in one")
	}
	if r.session(id).Started {
		t.Fatal("the refused Start work started")
	}

	s := newSandboxRig(t)
	id, a := planWithAgent(t, s.rig)
	if err := s.w.StartWork(context.Background(), StartWorkReq{Session: id, Sandbox: &on, Network: "open"}); err != nil {
		t.Fatal(err)
	}
	b := s.agent()
	if !a.Closed() || b.Spec.Sandbox == nil {
		t.Errorf("the work agent isn't in the sandbox: %+v", b.Spec.Sandbox)
	}
	if ag := s.session(id).Agent; !ag.Sandboxed || ag.Network != "open" || ag.Policy != "light" {
		t.Errorf("the agent after Start work: %+v", ag)
	}
	if box := s.keptBox(id); !box.cfg.NoEgressProxy {
		t.Error("the open network still goes through the egress proxy")
	}
}
