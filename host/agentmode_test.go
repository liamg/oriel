package host

import (
	"context"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/sandbox/mcp"
)

// A sandboxed agent switched into another mode during its turn (Claude's Shift+Tab) adds a project: the restart
// that lets it see the project resumes its conversation in that mode, not the one it was launched in.
func TestAProjectJoiningKeepsTheAgentsMode(t *testing.T) {
	s := newSandboxRig(t)
	id := s.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Work", Provider: "fake", Repos: []core.RepoID{core.RepoID(s.repo.Dir)},
		Sandbox: core.SandboxPolicy{Container: true, Network: "ask"}})
	a := s.agent()
	b := s.keptBox(id)
	other := gittest.New(t)
	other.Write(map[string]string{"README.md": "other\n"})
	other.Commit("Ada", "first")
	if _, err := s.w.addRepo(context.Background(), other.Dir, core.RepoAdded); err != nil {
		t.Fatal(err)
	}
	a.Emit(agent.TurnStart{})
	s.eventually(id, "working", func(se core.SessionState) bool { return se.Agent.Working })
	a.Emit(agent.ModeChanged{Mode: "auto"})
	res := make(chan mcp.CallResult, 1)
	go func() { res <- gatewayCall(t, b.Root(), "oriel__add_project", map[string]any{"repo": other.Dir}) }()
	ask := s.waitAsk(id, core.AskGateway)
	if err := s.w.Answer(AnswerReq{Session: id, Ask: ask.ID, Decision: "allow"}); err != nil {
		t.Fatal(err)
	}
	if r := <-res; r.IsError {
		t.Fatalf("add_project: %q", r.Text())
	}
	a.Emit(agent.TurnEnd{})
	again := s.agent()
	if got := s.fake.Resumed(); len(got) != 1 || got[0] != a.ID() {
		t.Fatalf("resumed %v, want %s", got, a.ID())
	}
	if again.Spec.Mode != "auto" {
		t.Errorf("the agent restarted in mode %q, want the auto it was switched to", again.Spec.Mode)
	}
}

// Every restart resumes the agent in the mode it last said it was in: one after it exits, and one after a hand over.
func TestRestartsKeepTheAgentsMode(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Work")
	a.WaitSent(t)
	r.eventually(id, "the agent's id", func(se core.SessionState) bool { return se.Agent.Session == a.ID() })
	a.Emit(agent.ModeChanged{Mode: "acceptEdits"})
	a.Handled(t)
	exitsAlone(a)
	a = r.agent()
	if a.Spec.Mode != "acceptEdits" {
		t.Errorf("restarted after it exited in mode %q, want acceptEdits", a.Spec.Mode)
	}
	r.eventually(id, "the agent running", func(se core.SessionState) bool { return se.Agent.Running })
	a.Emit(agent.ModeChanged{Mode: "plan"})
	a.Handled(t)
	r.w.HandOver()
	r.open()
	if a = r.agent(); a.Spec.Mode != "plan" {
		t.Errorf("resumed after a hand over in mode %q, want plan", a.Spec.Mode)
	}
}

// A fresh conversation starts in the policy's mode: the mode was the old conversation's.
func TestAFreshConversationDropsTheMode(t *testing.T) {
	r := newRig(t)
	id, a := planWithAgent(t, r)
	a.Emit(agent.ModeChanged{Mode: "plan"})
	a.Handled(t)
	if err := r.w.StartWork(context.Background(), StartWorkReq{Session: id}); err != nil {
		t.Fatal(err)
	}
	if fresh := r.agent(); fresh.Spec.Mode != "" {
		t.Errorf("a fresh conversation started in mode %q, want the policy's", fresh.Spec.Mode)
	}
}
