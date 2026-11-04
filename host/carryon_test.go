package host

import (
	"context"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
)

// replied is one turn of the agent's that replies on the session's request and ends.
func replied(r *rig, id core.SessionID, a *agenttest.FakeSession, args map[string]any) {
	r.t.Helper()
	args["thread"] = string(r.session(id).Threads[0].ID)
	a.Emit(agent.TurnStart{})
	a.Call(r.t, "reply", args)
	turn(r, id, a)
}

// A turn that ends with a thread still waiting on the agent, and nothing left running, is told to carry on: once for
// what it stands at, a few times at most without the user, and never when the agent has handed the thread back.
func TestATurnEndingWithWorkOwedIsToldToCarryOn(t *testing.T) {
	r := newRig(t)
	r.tools = []string{"reply", "commit_draft", "pr_draft"}
	id, a := r.workSession("Negative values round the wrong way")
	a.WaitSent(t)
	p, _ := r.session(id).Primary()
	write(t, p.Dir, "round.go", "package pricing\n")
	tid := r.session(id).Threads[0].ID

	replied(r, id, a, map[string]any{"body": "Writing the fix.", "working": true})
	got := a.WaitSent(t)
	for _, want := range []string{NoticeLead, "carry on", string(tid)} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("the nudge lacks %q:\n%s", want, got.Text)
		}
	}
	if strings.Contains(got.Text, "commit_draft") {
		t.Errorf("asked for drafts with the work owed:\n%s", got.Text)
	}
	turn(r, id, a)
	a.NoneSent(t) // nothing moved since

	replied(r, id, a, map[string]any{"body": "Still on it.", "working": true})
	a.WaitSent(t)
	replied(r, id, a, map[string]any{"body": "Nearly there.", "working": true})
	a.WaitSent(t)
	replied(r, id, a, map[string]any{"body": "One more step.", "working": true})
	a.NoneSent(t) // three without the user is enough

	if _, err := r.w.Comment(context.Background(), CommentReq{Session: id, Thread: tid, Body: "keep going"}); err != nil {
		t.Fatal(err)
	}
	a.WaitSent(t)
	replied(r, id, a, map[string]any{"body": "Working on it.", "working": true})
	a.WaitSent(t)

	// A subagent still running wakes it by itself.
	a.Emit(agent.TurnStart{})
	a.Emit(agent.Subagent{ID: "s1", Description: "Find the callers"})
	a.Call(t, "reply", map[string]any{"thread": string(tid), "body": "A subagent is finding the callers.", "working": true})
	turn(r, id, a)
	a.NoneSent(t)
	a.Emit(agent.Subagent{ID: "s1", Done: true})

	// Handed back to the user with a question: nothing owed, so the drafts are asked for.
	replied(r, id, a, map[string]any{"body": "Half-even or half-up?"})
	if got := a.WaitSent(t); strings.Contains(got.Text, "carry on") || !strings.Contains(got.Text, "commit_draft") {
		t.Errorf("after handing the thread back:\n%s", got.Text)
	}
}

// An agent started again after a restart, with work it owed and nothing new for it, is told it was cut off and
// to carry on; with nothing owed it's left alone.
func TestAResumedAgentCarriesOnWithWorkOwed(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Negative values round the wrong way")
	a.WaitSent(t)
	a.Emit(agent.TurnStart{})
	a.Call(t, "reply", map[string]any{"thread": string(r.session(id).Threads[0].ID), "body": "Writing the fix.", "working": true})
	r.eventually(id, "the agent's id", func(se core.SessionState) bool { return se.Agent.Session == a.ID() })
	r.restart()
	if err := r.w.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	b := r.agent()
	got := b.WaitSent(t)
	for _, want := range []string{"started you again", "carry on", string(r.session(id).Threads[0].ID)} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("the resumed agent's turn lacks %q:\n%s", want, got.Text)
		}
	}

	b.Emit(agent.TurnStart{})
	b.Call(t, "reply", map[string]any{"thread": string(r.session(id).Threads[0].ID), "body": "Fixed.", "resolve": true})
	b.Emit(agent.TurnEnd{})
	r.eventually(id, "the turn's end", func(se core.SessionState) bool { return !se.Agent.Working })
	r.restart()
	if err := r.w.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	r.agent().NoneSent(t)
}

// delivered waits for the host to mark what the agent was sent as delivered: a reply on a thread with a comment it
// hasn't had is refused.
func delivered(r *rig, id core.SessionID) core.SessionState {
	r.t.Helper()
	return r.eventually(id, "the agent's batch delivered", func(se core.SessionState) bool {
		for _, t := range se.Threads {
			for _, c := range t.Comments {
				if t.WaitsOnAgent() && c.Author == core.AuthorYou && !c.Delivered {
					return false
				}
			}
		}
		return true
	})
}

// answered has the agent reply on and resolve every thread that waits on it, as one that finished its batch does, so
// it owes nothing.
func answered(r *rig, id core.SessionID, a *agenttest.FakeSession) {
	r.t.Helper()
	for _, t := range delivered(r, id).Threads {
		if t.WaitsOnAgent() {
			a.Call(r.t, "reply", map[string]any{"thread": string(t.ID), "body": "Done.", "resolve": true})
		}
	}
}
