package rpcagent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/isolate"
)

// behind is p behind the protocol: Serve on one end of a pipe, a Client dialled on the other.
func behind(t *testing.T, name string, p agent.Provider) agent.Provider {
	t.Helper()
	isolate.Test(t)
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Serve(br, bw, func(Init) (agent.Provider, error) { return p, nil }) }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, Options{Name: name, Dial: Pipe(ar, aw)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closer(c).Close()
		bw.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return c
}

func closer(p agent.Provider) interface{ Close() error } {
	switch c := p.(type) {
	case *Client:
		return c
	case logged_:
		return c.Client
	case discovered_:
		return c.Client
	case loggedDiscovered:
		return c.Client
	}
	panic("not a client")
}

func harness(p agent.Provider) agenttest.Harness {
	return agenttest.Harness{Provider: p, Spec: func(t *testing.T, dir string) agent.StartSpec {
		return agent.StartSpec{Exec: hostexec.Exec{}, Policy: agent.PolicyAsk, StateDir: t.TempDir()}
	}}
}

// The scripted provider plays the suite by itself, so it can stand behind the protocol.
func TestScriptedConformance(t *testing.T) {
	agenttest.Run(t, harness(&agenttest.Scripted{}))
}

// Every scenario of the suite holds for a provider served over the protocol: its tools are called back, its
// processes run through Oriel's Exec, its terminal and resumes cross.
func TestConformanceOverTheProtocol(t *testing.T) {
	agenttest.Run(t, harness(behind(t, "scripted", &agenttest.Scripted{})))
}

// What a provider says crosses whole: every event type with every field set, as the fake says it.
func TestEventsCrossWhole(t *testing.T) {
	f := agenttest.NewFake()
	f.Name = "plug"
	c := behind(t, "plug", f)
	ctx := context.Background()
	s, err := c.Start(ctx, agent.StartSpec{Policy: agent.PolicyAsk, Mode: "auto", Allow: []string{"WebSearch"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fs := f.WaitStarted(t)
	if fs.Spec.Policy != agent.PolicyAsk || fs.Spec.Mode != "auto" || !slices.Equal(fs.Spec.Allow, []string{"WebSearch"}) {
		t.Errorf("the provider started on policy %q, mode %q, allowing %q; want ask, auto, WebSearch", fs.Spec.Policy, fs.Spec.Mode, fs.Spec.Allow)
	}
	first := s.ID()
	diff := &core.FileDiff{Path: "a.go", Added: 1, Removed: 1, Lines: []core.DiffLine{{Op: "-", Old: 3, Text: "x"}, {Op: "+", New: 3, Text: "y"}}}
	evs := []agent.Event{
		agent.Ready{ID: "again", Model: "m2"},
		agent.TurnStart{},
		agent.Message{ID: "m1", Role: core.ItemAssistant, Text: "hel", Partial: true, Agent: "sub1"},
		agent.Message{ID: "m1", Role: core.ItemThinking, Text: "hello"},
		agent.ToolCall{ID: "c1", Tool: "Bash", Input: json.RawMessage(`{"command":"ls"}`), Agent: "sub1"},
		agent.ToolResult{ID: "c1", Output: "out", IsError: true, Diff: diff},
		agent.Permission{ID: "p1", Tool: "Bash", Summary: "ls", Input: json.RawMessage(`{"command":"ls"}`), Rule: "Bash(ls:*)", Diff: diff},
		agent.Question{ID: "q1", Questions: []core.Question{{Header: "H", Question: "Q?", Options: []string{"a", "b"}, Multi: true}}, Thread: "t1"},
		agent.Subagent{ID: "sub1", Description: "d", Type: "general", Prompt: "p", Done: true, Result: "r"},
		agent.TurnEnd{Usage: agent.Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, Context: 5, ContextMax: 6, Cost: 0.5,
			Duration: 1500 * time.Millisecond}, Background: []core.BackgroundTask{{Kind: "shell", Description: "go test ./..."}},
			Running: []core.BackgroundTask{{ID: "p1", Kind: "terminal", Description: "npm run dev", PID: 4242, CPU: 1.5, Memory: 2 << 20}}},
		agent.Failure{Err: "overloaded", Retryable: true},
		agent.UsageUpdate{Usage: core.ProviderUsage{Provider: "plug", Account: "a", Plan: "Pro", Windows: []core.UsageWindow{{Name: "5h", Used: 0.5}}}},
		agent.AuthExpired{Provider: "plug", Account: "a", Detail: "revoked", Terminal: "/login", Shell: "plug login"},
		agent.AuthRestored{Provider: "plug", Account: "a"},
		agent.Dialog{Title: "Allow it?", Shown: true},
		agent.Dialog{},
		agent.ModeChanged{Mode: "auto"},
		agent.Exited{Err: "gone"},
	}
	for _, ev := range evs {
		fs.Emit(ev)
	}
	var got []agent.Event
	timeout := time.After(10 * time.Second)
	for len(got) < len(evs)+1 {
		select {
		case ev := <-s.Events():
			got = append(got, ev)
		case <-timeout:
			t.Fatalf("got %d events: %+v", len(got), got)
		}
	}
	if r, ok := got[0].(agent.Ready); !ok || r.ID != fs.ID() || first != fs.ID() || s.ID() != "again" {
		t.Errorf("first = %+v, id %q then %q; want Ready %s, then again", got[0], first, s.ID(), fs.ID())
	}
	for i, want := range evs {
		w, _ := json.Marshal(want)
		g, _ := json.Marshal(got[i+1])
		if !reflect.DeepEqual(got[i+1], want) {
			t.Errorf("event %d: got %T %s, want %T %s", i, got[i+1], g, want, w)
		}
	}
}

// Stopping a process a turn left running crosses to the provider's session, as its features say it can; one whose
// sessions can't refuses.
func TestStopProcessCrosses(t *testing.T) {
	f := agenttest.NewFake()
	f.Has = &agent.Features{StopProcesses: true}
	c := behind(t, "fake", f)
	if !agent.FeaturesOf(c).StopProcesses {
		t.Errorf("features = %+v, want StopProcesses", agent.FeaturesOf(c))
	}
	s, err := c.Start(context.Background(), agent.StartSpec{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fs := f.WaitStarted(t)
	ps, ok := s.(agent.ProcessStopper)
	if !ok {
		t.Fatal("a client's session isn't a ProcessStopper")
	}
	if err := ps.StopProcess(context.Background(), "p1"); err != nil {
		t.Fatal(err)
	}
	if got := fs.Stopped(); !reflect.DeepEqual(got, []string{"p1"}) {
		t.Errorf("stopped %v, want [p1]", got)
	}
	g := agenttest.NewFake()
	g.Has = &agent.Features{}
	s2, err := behind(t, "fake", g).Start(context.Background(), agent.StartSpec{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.(agent.ProcessStopper).StopProcess(context.Background(), "p2"); err == nil {
		t.Error("a session that can't stop processes stopped one")
	}
}

// A provider's id stands for Oriel's name for it, wherever an event names it.
func TestEventsNameTheClient(t *testing.T) {
	f := agenttest.NewFake()
	c := behind(t, "mine", f)
	s, err := c.Start(context.Background(), agent.StartSpec{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fs := f.WaitStarted(t)
	fs.LoseLogin("revoked")
	for ev := range s.Events() {
		if a, ok := ev.(agent.AuthExpired); ok {
			if a.Provider != "mine" || c.ID() != "mine" {
				t.Errorf("AuthExpired names %q; the client is %q", a.Provider, c.ID())
			}
			return
		}
	}
}

// The provider's own calls cross: what Detect found, its login mark too; its models; a refused start's error.
func TestProviderCallsCross(t *testing.T) {
	f := agenttest.NewFake()
	f.Account = "acct"
	f.Offers = []agent.Model{{ID: "m", Name: "M", Efforts: []string{"low"}, Context: 1000, Default: true, Small: true}}
	c := behind(t, "fake", f)
	ctx := context.Background()
	want, _ := f.Detect(ctx)
	got, err := c.Detect(ctx)
	if err != nil || got != want || got.Login == "" {
		t.Errorf("Detect = %+v %v, want %+v", got, err, want)
	}
	ms, err := c.Models(ctx)
	if err != nil || !reflect.DeepEqual(ms, f.Offers) {
		t.Errorf("Models = %+v %v", ms, err)
	}
	u, err := c.Usage(ctx)
	if err != nil || u.Account != "acct" || u.Provider != "fake" {
		t.Errorf("Usage = %+v %v", u, err)
	}
	expired := agent.AuthExpired{Provider: "fake", Detail: "signed out", Shell: "fake login", HostCommand: []string{"fake", "login"}}
	f.Refuse(expired)
	_, err = c.Start(ctx, agent.StartSpec{})
	var ae agent.AuthExpired
	if !errors.As(err, &ae) || !reflect.DeepEqual(ae, expired) {
		t.Errorf("a start refused for the login: %v (%T)", err, err)
	}
	f.Refuse(nil)
}

// A session's Exec runs on Oriel's side: the provider starts its process through the spec, and Oriel's Exec gets it.
func TestExecRunsOnOriel(t *testing.T) {
	f := agenttest.NewFake()
	f.Launch = true
	c := behind(t, "fake", f)
	rec := &recordExec{}
	s, err := c.Start(context.Background(), agent.StartSpec{Exec: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := rec.started(); len(got) != 1 || got[0] != "fake" {
		t.Errorf("Oriel's Exec ran %v, want the agent's own process", got)
	}
}

// A program the plugin runs outside a sandbox needs Allow's yes.
func TestExecOutsideASandboxIsAllowed(t *testing.T) {
	isolate.Test(t)
	f := agenttest.NewFake()
	f.Name, f.Launch = "true", true // it launches a program of its name
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Serve(br, bw, func(Init) (agent.Provider, error) { return f, nil }) }()
	var asked []string
	c := New(Options{Name: "true", Dial: Pipe(ar, aw), Allow: func(path string) error {
		asked = append(asked, path)
		return errors.New("exec isn't granted for " + path)
	}})
	defer func() { closer(c).Close(); bw.Close(); <-done }()
	_, err := c.Start(context.Background(), agent.StartSpec{Exec: &recordExec{}})
	if err == nil || !strings.Contains(err.Error(), "isn't granted") || len(asked) != 1 || !strings.HasSuffix(asked[0], "/true") {
		t.Errorf("start = %v, asked %v", err, asked)
	}
	if _, err := c.Start(context.Background(), agent.StartSpec{Exec: &recordExec{}, Sandbox: &agent.Sandbox{Name: "box"}}); err != nil {
		t.Errorf("in a sandbox: %v", err)
	}
}

// recordExec runs true for any command, and says what it was asked to run.
type recordExec struct {
	mu   sync.Mutex
	runs []string
}

func (r *recordExec) started() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.runs...)
}

func (r *recordExec) StartPTY(context.Context, *exec.Cmd, int, int) (agent.PTY, error) {
	return nil, errors.New("no pty")
}

func (r *recordExec) Start(ctx context.Context, cmd *exec.Cmd) (agent.Process, error) {
	r.mu.Lock()
	r.runs = append(r.runs, cmd.Args[0])
	r.mu.Unlock()
	return hostexec.Exec{}.Start(ctx, exec.Command("true"))
}

// A session's terminal crosses: what it showed before a view attached, what it shows live, and what's typed in it.
func TestTerminalCrosses(t *testing.T) {
	c := behind(t, "scripted", &agenttest.Scripted{})
	s, err := c.Start(context.Background(), agent.StartSpec{Exec: hostexec.Exec{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	term := s.Terminal()
	if term == nil {
		t.Fatal("no terminal")
	}
	// shows reads term, from its backlog on, until it has shown want.
	shows := func(want string) {
		t.Helper()
		b, live, detach := term.Attach()
		defer detach()
		got := string(b)
		hung := time.After(10 * time.Second)
		for !strings.Contains(got, want) {
			select {
			case b := <-live:
				got += string(b)
			case <-hung:
				t.Fatalf("the terminal showed %q", got)
			}
		}
	}
	if _, err := term.Write([]byte("before\r")); err != nil {
		t.Fatal(err)
	}
	if err := term.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	shows("before")
	if b, _, detach := term.Attach(); !strings.Contains(string(b), "before") {
		t.Errorf("once shown, the backlog is %q", b)
	} else {
		detach()
	}
	term.Write([]byte("after\r"))
	shows("after")
}
