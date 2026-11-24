package agentui_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/headless"
)

// An agent's reply that proposes a change offers Do it in its thread: a click is your "Do it", sent to the agent, and
// the offer goes with it. A reply that proposes nothing offers nothing.
func TestAProposalIsDoneWithAClick(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	r.Run("request.comment", registry.Args{"body": "Is the retry loop right?"})
	a.WaitSent(t)
	th := threadWith(t, r, "Is the retry loop right?")
	if out := a.Call(t, "reply", map[string]any{"thread": string(th.ID), "body": "It's fine as it is.", "working": false}); out != "replied" {
		t.Fatalf("reply said %q", out)
	}
	a.Emit(agent.TurnEnd{})
	r.Run("request.open", registry.Args{"thread": string(th.ID)})
	r.WaitText("It's fine as it is.")
	f := r.Latest("the reply in the thread", func(f headless.Frame) bool { return strings.Contains(f.Text(), "It's fine as it is.") })
	if inThread(r, "Conversation", "Do it") {
		t.Errorf("a reply that proposes nothing offers Do it:\n%s", f.Text())
	}

	r.Run("request.comment", registry.Args{"thread": string(th.ID), "body": "It never backs off."})
	a.WaitSent(t)
	a.Call(t, "reply", map[string]any{"thread": string(th.ID), "body": "I'd double the delay each attempt, capped at 30s.", "proposes": true})
	a.Emit(agent.TurnEnd{})
	pickInThread(t, r, "Conversation", "Do it")
	if in := a.WaitSent(t); !strings.Contains(in.Text, "Do it") {
		t.Errorf("the agent was sent %q", in.Text)
	}
	if last, _ := threadWith(t, r, "Is the retry loop right?").Last(); last.Author != core.AuthorYou || last.Body != "Do it" {
		t.Errorf("the thread ends %+v, want your Do it", last)
	}
	cardGone(r, "Conversation", "Do it")
}
