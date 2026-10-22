// Package agenttest is the provider conformance suite: every provider runs it against a fake of its CLI that plays
// the scenarios below, and on demand against the real one.
//
// The fakes answer these prompts, and no others:
//
//	"say hello"            replies "hello" and ends the turn
//	"call echo"            calls the Oriel tool "echo" with {"text":"hi"}, then replies with what it returned
//	"run a command"        asks permission for a shell command "go test ./..."; allowed, it runs it and replies "ran:
//	                       ok"; denied, it replies "denied: " and the denial's note
//	"ask me"               asks one question, "Which colour?" with options red and blue, and replies with the answer
//	"work slowly"          starts a turn that only ends when interrupted
//	"what did I say"       replies with the prompt before this one: after a resume, that proves the history came back
//	"lose the login"       fails the turn with the provider's own signed-out error
//
// A fork replies "fork: " and its prompt.
package agenttest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/isolate"
)

// Harness starts the provider under test.
type Harness struct {
	Provider agent.Provider
	// Spec is the start spec for a session in dir: the fake's binary, its environment, a state directory. The
	// suite adds the tools and the prompt.
	Spec func(t *testing.T, dir string) agent.StartSpec
	// Timeout bounds each wait; a live run needs longer than a fake.
	Timeout time.Duration
}

// Run runs every scenario.
func Run(t *testing.T, h Harness) {
	if h.Timeout == 0 {
		h.Timeout = 20 * time.Second
	}
	t.Run("start and send", func(t *testing.T) { startAndSend(t, h) })
	t.Run("tool call", func(t *testing.T) { toolCall(t, h) })
	t.Run("permission allowed", func(t *testing.T) { permission(t, h, true) })
	t.Run("permission denied", func(t *testing.T) { permission(t, h, false) })
	t.Run("question", func(t *testing.T) { question(t, h) })
	t.Run("interrupt", func(t *testing.T) { interrupt(t, h) })
	t.Run("fork", func(t *testing.T) { fork(t, h) })
	t.Run("resume", func(t *testing.T) { resume(t, h) })
	t.Run("login expired", func(t *testing.T) { loginExpired(t, h) })
}

// Echo is the tool every session is offered.
var Echo = agent.Tool{
	Name:        "echo",
	Description: "Say text back.",
	Schema:      json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
	Call: func(_ context.Context, args json.RawMessage) (string, error) {
		var a struct{ Text string }
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		return "echo: " + a.Text, nil
	},
}

// Session is a started session and what it has said so far.
type Session struct {
	t      *testing.T
	h      Harness
	S      agent.Session
	Events []agent.Event
}

// Start starts a session in a fresh directory.
func Start(t *testing.T, h Harness) *Session {
	t.Helper()
	return StartIn(t, h, t.TempDir(), "")
}

// StartIn starts a session in dir, resuming id when it isn't "".
func StartIn(t *testing.T, h Harness, dir, id string) *Session {
	t.Helper()
	isolate.Test(t)
	spec := h.Spec(t, dir)
	spec.Cwd = dir
	spec.Tools = append(spec.Tools, Echo)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var s agent.Session
	var err error
	if id == "" {
		s, err = h.Provider.Start(ctx, spec)
	} else {
		s, err = h.Provider.Resume(ctx, id, spec)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return &Session{t: t, h: h, S: s}
}

// Wait reads events until match says so, and returns the one it matched.
func (s *Session) Wait(what string, match func(agent.Event) bool) agent.Event {
	s.t.Helper()
	deadline := time.After(s.h.Timeout)
	for {
		select {
		case ev, ok := <-s.S.Events():
			if !ok {
				s.t.Fatalf("waiting for %s: the session's events ended; saw %s", what, describe(s.Events))
			}
			s.Events = append(s.Events, ev)
			if match(ev) {
				return ev
			}
		case <-deadline:
			s.t.Fatalf("waiting for %s: timed out; saw %s", what, describe(s.Events))
		}
	}
}

// Send sends a prompt.
func (s *Session) Send(text string) {
	s.t.Helper()
	if err := s.S.Send(context.Background(), agent.Input{Text: text}); err != nil {
		s.t.Fatal(err)
	}
}

// Reply waits for the turn to end and returns everything the agent said in it, joined.
func (s *Session) Reply() string {
	s.t.Helper()
	from := len(s.Events)
	s.Wait("the turn to end", func(ev agent.Event) bool { _, ok := ev.(agent.TurnEnd); return ok })
	var said []string
	for _, ev := range s.Events[from:] {
		if m, ok := ev.(agent.Message); ok && m.Role == core.ItemAssistant && !m.Partial && m.Agent == "" {
			said = append(said, m.Text)
		}
	}
	return strings.Join(said, "\n")
}

func startAndSend(t *testing.T, h Harness) {
	s := Start(t, h)
	s.Send("say hello")
	s.Wait("the turn to start", func(ev agent.Event) bool { _, ok := ev.(agent.TurnStart); return ok })
	if got := s.Reply(); !strings.Contains(got, "hello") {
		t.Errorf("reply = %q, want hello", got)
	}
	if s.S.ID() == "" {
		t.Error("the session has no id to resume by")
	}
}

func toolCall(t *testing.T, h Harness) {
	s := Start(t, h)
	s.Send("call echo")
	call := s.Wait("the echo call", func(ev agent.Event) bool {
		c, ok := ev.(agent.ToolCall)
		return ok && strings.HasSuffix(c.Tool, "echo")
	}).(agent.ToolCall)
	res := s.Wait("its result", func(ev agent.Event) bool { r, ok := ev.(agent.ToolResult); return ok && r.ID == call.ID }).(agent.ToolResult)
	if res.IsError || !strings.Contains(res.Output, "echo: hi") {
		t.Errorf("result = %+v", res)
	}
	if got := s.Reply(); !strings.Contains(got, "echo: hi") {
		t.Errorf("reply = %q, want the tool's answer", got)
	}
}

func permission(t *testing.T, h Harness, allow bool) {
	s := Start(t, h)
	s.Send("run a command")
	p := s.Wait("the permission", func(ev agent.Event) bool { _, ok := ev.(agent.Permission); return ok }).(agent.Permission)
	if !strings.Contains(p.Summary+string(p.Input), "go test") {
		t.Errorf("permission = %+v, want the command in it", p)
	}
	note := "use the Makefile target"
	if err := s.S.Answer(context.Background(), p.ID, agent.Decision{Allow: allow, Note: note}); err != nil {
		t.Fatal(err)
	}
	got := s.Reply()
	switch {
	case allow && !strings.Contains(got, "ran: ok"):
		t.Errorf("allowed, the reply is %q", got)
	case !allow && !strings.Contains(got, note):
		t.Errorf("denied, the reply is %q; the agent should have read the note", got)
	}
}

func question(t *testing.T, h Harness) {
	s := Start(t, h)
	s.Send("ask me")
	q := s.Wait("the question", func(ev agent.Event) bool { _, ok := ev.(agent.Question); return ok }).(agent.Question)
	if len(q.Questions) != 1 || q.Questions[0].Question != "Which colour?" {
		t.Fatalf("question = %+v", q)
	}
	if err := s.S.Answer(context.Background(), q.ID, agent.Decision{Answers: map[string]string{"Which colour?": "blue"}}); err != nil {
		t.Fatal(err)
	}
	if got := s.Reply(); !strings.Contains(got, "blue") {
		t.Errorf("reply = %q, want the answer", got)
	}
}

func interrupt(t *testing.T, h Harness) {
	s := Start(t, h)
	s.Send("work slowly")
	s.Wait("the turn to start", func(ev agent.Event) bool { _, ok := ev.(agent.TurnStart); return ok })
	if err := s.S.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Wait("the turn to end", func(ev agent.Event) bool { _, ok := ev.(agent.TurnEnd); return ok })
	s.Send("say hello")
	if got := s.Reply(); !strings.Contains(got, "hello") {
		t.Errorf("after an interrupt, the reply is %q", got)
	}
}

func fork(t *testing.T, h Harness) {
	s := Start(t, h)
	s.Send("say hello")
	s.Reply()
	ctx, cancel := context.WithTimeout(context.Background(), h.Timeout)
	defer cancel()
	got, err := s.S.Fork(ctx, agent.ForkSpec{Prompt: "why decimal?"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "why decimal?") {
		t.Errorf("fork = %q", got)
	}
	// The fork stays out of the main conversation.
	s.Send("what did I say")
	if got := s.Reply(); !strings.Contains(got, "say hello") {
		t.Errorf("after a fork, the main conversation remembers %q", got)
	}
}

func resume(t *testing.T, h Harness) {
	dir := t.TempDir()
	s := StartIn(t, h, dir, "")
	s.Send("say hello")
	s.Reply()
	id := s.S.ID()
	if err := s.S.Close(); err != nil {
		t.Fatal(err)
	}
	r := StartIn(t, h, dir, id)
	if r.S.ID() != id {
		t.Errorf("resumed as %q, want %q", r.S.ID(), id)
	}
	r.Send("what did I say")
	if got := r.Reply(); !strings.Contains(got, "say hello") {
		t.Errorf("resumed, the reply is %q; the history didn't come back", got)
	}
}

// loginExpired: a signed-out error raises AuthExpired, naming the provider and how to sign in again.
func loginExpired(t *testing.T, h Harness) {
	s := Start(t, h)
	s.Send("lose the login")
	ev := s.Wait("AuthExpired", func(ev agent.Event) bool { _, ok := ev.(agent.AuthExpired); return ok }).(agent.AuthExpired)
	if ev.Provider != h.Provider.ID() || ev.Detail == "" {
		t.Errorf("AuthExpired = %+v, want the provider and what it said", ev)
	}
	if ev.Terminal == "" && ev.Shell == "" {
		t.Errorf("AuthExpired = %+v says no way to sign in again", ev)
	}
	s.Wait("the turn to end", func(ev agent.Event) bool { _, ok := ev.(agent.TurnEnd); return ok })
}

func describe(evs []agent.Event) string {
	var b strings.Builder
	for _, ev := range evs {
		fmt.Fprintf(&b, "\n  %T %+v", ev, ev)
	}
	if b.Len() == 0 {
		return "nothing"
	}
	return b.String()
}
