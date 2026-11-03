package host

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
)

func nextUtility(t *testing.T, u *Utility) UtilityEvent {
	t.Helper()
	select {
	case ev, ok := <-u.Events():
		if !ok {
			t.Fatal("the utility agent's stream ended")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event from the utility agent")
	}
	return UtilityEvent{}
}

// Outside a session (the assistant …), a resident utility session: a hidden interactive claude in a PTY with the
// Oriel mod, a small model, read-only tools, and a fresh context per task. It starts through the
// provider like any session (so Claude is its interactive CLI), offered only the app's tools for it.
func TestUtilityAgent(t *testing.T) {
	r := newRig(t)
	r.fake.Offers = []agent.Model{{ID: "big", Default: true}, {ID: "small", Small: true}}
	var asked ToolsReq
	calls := make(chan ToolCall, 1)
	r.w.SetApp(App{
		Tools: func(q ToolsReq) []agent.Tool {
			asked = q
			return []agent.Tool{{Name: "sessions", Description: "Every session.", ReadOnly: true, Schema: json.RawMessage(`{"type":"object"}`)}}
		},
		CallTool: func(_ context.Context, call ToolCall) (string, error) {
			calls <- call
			return "rounding: needs you", nil
		}})
	if _, err := r.w.StartUtility(context.Background(), UtilitySpec{Name: "../x", Pack: "p", Prompt: "hi"}); err == nil {
		t.Error("a utility agent named ../x started")
	}
	u, err := r.w.StartUtility(context.Background(), UtilitySpec{Name: "assistant", Pack: "Be brief.", Prompt: "what needs me?",
		Web: true})
	if err != nil {
		t.Fatal(err)
	}
	fs := r.fake.WaitStarted(t)
	if ready := u.Ready(); ready.Type != "ready" || ready.ID == "" || ready.Provider != "fake" || ready.Model != "small" {
		t.Errorf("ready: %+v", ready)
	}
	s := fs.Spec
	if s.Model != "small" || s.Pack.Skill != "assistant" || s.Pack.Text != "Be brief." || s.Prompt != "what needs me?" ||
		s.Policy != agent.PolicyAsk || !s.Unattended || s.Cwd != filepath.Join(r.home, "utility", "assistant") {
		t.Errorf("started with %+v", s)
	}
	if in := fs.WaitSent(t); in.Text != "what needs me?" {
		t.Errorf("its first turn was %q", in.Text)
	}
	if asked.Agent != "assistant" || asked.Session != "" {
		t.Errorf("the app was asked for the tools of %+v", asked)
	}
	if len(s.Tools) != 1 || s.Tools[0].Name != "sessions" {
		t.Fatalf("tools: %+v", s.Tools)
	}
	if got := fs.Call(t, "sessions", map[string]any{}); got != "rounding: needs you" {
		t.Errorf("the tool answered %q", got)
	}
	if call := <-calls; call.Agent != "assistant" || call.Session != "" || call.Tool != "sessions" {
		t.Errorf("the call reached the app as %+v", call)
	}

	fs.Emit(agent.TurnStart{})
	fs.Emit(agent.ToolCall{ID: "t1", Tool: "mcp__oriel__sessions", Input: json.RawMessage(`{}`)})
	fs.Emit(agent.Message{Role: core.ItemAssistant, Text: "One thing: rounding.", Partial: true})
	fs.Emit(agent.Message{Role: core.ItemAssistant, Text: "One thing: rounding."})
	fs.Emit(agent.TurnEnd{})
	for _, want := range []UtilityEvent{{Type: "turn-start"}, {Type: "tool-call", ID: "t1", Tool: "sessions"},
		{Type: "message", Text: "One thing: rounding."}, {Type: "turn-end"}} {
		got := nextUtility(t, u)
		if got.Type != want.Type || got.Tool != want.Tool || got.Text != want.Text || got.ID != want.ID {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}

	// It may search the web with the provider's own web tools; anything else it asks for is refused, and nobody
	// answers its questions.
	if d := <-fs.Ask(agent.Permission{ID: "p1", Tool: "WebSearch"}); !d.Allow {
		t.Error("WebSearch was refused")
	}
	if d := <-fs.Ask(agent.Permission{ID: "p2", Tool: "Bash"}); d.Allow || d.Note == "" {
		t.Errorf("Bash: %+v", d)
	}

	if err := u.Send(context.Background(), "and the PRs?"); err != nil {
		t.Fatal(err)
	}
	if in := fs.WaitSent(t); in.Text != "and the PRs?" {
		t.Errorf("sent %q", in.Text)
	}
	u.Close()
	deadline := time.Now().Add(5 * time.Second)
	for !fs.Closed() {
		if time.Now().After(deadline) {
			t.Fatal("the agent outlived its stream")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := u.Send(context.Background(), "still there?"); err == nil {
		t.Error("a stopped utility agent took a turn")
	}
}

// A session's agent isn't offered the tools of a utility agent: the host asks the app for the session's kind's.
func TestSessionToolsAreAskedForByKind(t *testing.T) {
	r := newRig(t)
	var asked ToolsReq
	r.w.SetApp(App{Tools: func(q ToolsReq) []agent.Tool {
		asked = q
		return nil
	}})
	id, _ := r.workSession("Work")
	if asked.Session != id || asked.Agent != string(core.KindWork) {
		t.Errorf("asked for %+v", asked)
	}
}

// A utility agent whose start panics fails to start; StartUtility doesn't wait on it forever.
func TestUtilityAgentThatPanicsFailsToStart(t *testing.T) {
	r := newRig(t, panicky{&agenttest.Fake{Name: "codex"}})
	done := make(chan error, 1)
	go func() {
		_, err := r.w.StartUtility(context.Background(), UtilitySpec{Name: "assistant", Provider: "codex", Pack: "p", Prompt: "hi"})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a utility agent whose start panicked started")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StartUtility is still waiting")
	}
}
