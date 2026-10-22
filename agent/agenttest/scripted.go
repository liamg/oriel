package agenttest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

// Scripted is a provider that plays the conformance scenarios itself, for a test of what wraps a provider (a
// protocol, a bridge). Its processes run through the spec's Exec: a command it's allowed to run, and its terminal,
// a PTY running cat.
type Scripted struct {
	Name string // its ID; "scripted" if ""

	mu      sync.Mutex
	seq     int
	history map[string][]string // what each session was told, by id
}

var (
	_ agent.Provider = (*Scripted)(nil)
	_ agent.Featured = (*Scripted)(nil)
)

func (p *Scripted) ID() string {
	if p.Name == "" {
		return "scripted"
	}
	return p.Name
}

func (p *Scripted) Features() agent.Features { return agent.Features{Terminal: true, Fork: true} }

func (p *Scripted) Detect(context.Context) (agent.Install, error) {
	return agent.Install{Bin: "scripted", Version: "1", LoggedIn: true, Login: "login-1"}, nil
}

func (p *Scripted) Models(context.Context) ([]agent.Model, error) {
	return []agent.Model{{ID: "s1", Name: "Scripted", Default: true}}, nil
}

func (p *Scripted) Usage(context.Context) (core.ProviderUsage, error) {
	return core.ProviderUsage{Provider: p.ID()}, nil
}

func (p *Scripted) Start(ctx context.Context, s agent.StartSpec) (agent.Session, error) {
	p.mu.Lock()
	p.seq++
	id := fmt.Sprintf("%s-%d", p.ID(), p.seq)
	p.mu.Unlock()
	return p.open(ctx, id, s)
}

func (p *Scripted) Resume(ctx context.Context, id string, s agent.StartSpec) (agent.Session, error) {
	p.mu.Lock()
	_, ok := p.history[id]
	p.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no session %s to resume", id)
	}
	return p.open(ctx, id, s)
}

func (p *Scripted) open(ctx context.Context, id string, spec agent.StartSpec) (*scripted, error) {
	p.mu.Lock()
	if p.history == nil {
		p.history = map[string][]string{}
	}
	if _, ok := p.history[id]; !ok {
		p.history[id] = nil
	}
	p.mu.Unlock()
	s := &scripted{p: p, id: id, spec: spec, ctx: ctx, ev: make(chan agent.Event, 256), answers: map[string]chan agent.Decision{},
		interrupt: make(chan struct{}, 1)}
	if spec.Exec != nil {
		pty, err := spec.Exec.StartPTY(ctx, exec.Command("cat"), 80, 24)
		if err != nil {
			return nil, err
		}
		s.term = &ptyTerminal{pty: pty, viewers: map[chan []byte]struct{}{}, drained: make(chan struct{})}
		go s.term.drain()
	}
	s.emit(agent.Ready{ID: id})
	if spec.Prompt != "" {
		s.Send(ctx, agent.Input{Text: spec.Prompt})
	}
	return s, nil
}

func (p *Scripted) said(id, prompt string) (before string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h := p.history[id]
	if len(h) > 0 {
		before = h[len(h)-1]
	}
	p.history[id] = append(h, prompt)
	return before
}

type scripted struct {
	p    *Scripted
	id   string
	spec agent.StartSpec
	ctx  context.Context
	term *ptyTerminal

	mu        sync.Mutex
	ev        chan agent.Event
	closed    bool
	turn      sync.Mutex // one turn at a time
	seq       int
	answers   map[string]chan agent.Decision
	interrupt chan struct{}
	replied   bool
}

func (s *scripted) ID() string { return s.id }

func (s *scripted) emit(ev agent.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.ev <- ev
	}
}

func (s *scripted) next(kind string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return fmt.Sprintf("%s-%d", kind, s.seq)
}

func (s *scripted) say(text string) {
	id := s.next("m")
	if len(text) > 1 {
		s.emit(agent.Message{ID: id, Role: core.ItemAssistant, Text: text[:len(text)/2], Partial: true})
	}
	s.emit(agent.Message{ID: id, Role: core.ItemAssistant, Text: text})
	s.mu.Lock()
	s.replied = true
	s.mu.Unlock()
}

func (s *scripted) ask(ev agent.Event, id string) agent.Decision {
	ch := make(chan agent.Decision, 1)
	s.mu.Lock()
	s.answers[id] = ch
	s.mu.Unlock()
	s.emit(ev)
	return <-ch
}

func (s *scripted) Send(_ context.Context, in agent.Input) error {
	before := s.p.said(s.id, in.Text)
	go func() {
		s.turn.Lock()
		defer s.turn.Unlock()
		select {
		case <-s.interrupt:
		default:
		}
		s.emit(agent.TurnStart{})
		s.play(in.Text, before)
		s.emit(agent.TurnEnd{Usage: agent.Usage{Input: int64(len(in.Text)), Output: 1}})
	}()
	return nil
}

func (s *scripted) play(prompt, before string) {
	switch prompt {
	case "say hello":
		s.say("hello")
	case "call echo":
		id := s.next("call")
		args := json.RawMessage(`{"text":"hi"}`)
		s.emit(agent.ToolCall{ID: id, Tool: "mcp__oriel__echo", Input: args})
		out, err := "", fmt.Errorf("no tool echo")
		for _, t := range s.spec.Tools {
			if t.Name == "echo" && t.Call != nil {
				out, err = t.Call(s.ctx, args)
			}
		}
		if err != nil {
			s.emit(agent.ToolResult{ID: id, Output: err.Error(), IsError: true})
			s.say(err.Error())
			return
		}
		s.emit(agent.ToolResult{ID: id, Output: out})
		s.say(out)
	case "run a command":
		id := s.next("perm")
		cmd := json.RawMessage(`{"command":"go test ./..."}`)
		d := s.ask(agent.Permission{ID: id, Tool: "Bash", Summary: "go test ./...", Input: cmd, Rule: "Bash(go test:*)"}, id)
		if !d.Allow {
			s.say("denied: " + d.Note)
			return
		}
		s.emit(agent.ToolCall{ID: id, Tool: "Bash", Input: cmd})
		out, err := s.run()
		s.emit(agent.ToolResult{ID: id, Output: out, IsError: err != nil})
		s.say("ran: " + out)
	case "ask me":
		id := s.next("q")
		d := s.ask(agent.Question{ID: id, Questions: []core.Question{{Question: "Which colour?", Options: []string{"red", "blue"}}}}, id)
		s.say("you chose " + d.Answers["Which colour?"])
	case "work slowly":
		s.mu.Lock()
		ch := s.interrupt
		s.mu.Unlock()
		select {
		case <-ch:
		case <-s.ctx.Done():
		}
	case "what did I say":
		s.say("you said: " + before)
	case "lose the login":
		s.emit(agent.Failure{Err: "signed out"})
		s.emit(agent.AuthExpired{Provider: s.p.ID(), Detail: "signed out", Shell: s.p.ID() + " login"})
	default:
		s.say("I don't know " + prompt)
	}
}

// run runs the allowed command through the spec's Exec, as the agent would: what it prints is "ok".
func (s *scripted) run() (string, error) {
	if s.spec.Exec == nil {
		return "ok", nil
	}
	p, err := s.spec.Exec.Start(s.ctx, exec.Command("sh", "-c", "echo ok"))
	if err != nil {
		return err.Error(), err
	}
	p.Stdin().Close()
	b, _ := io.ReadAll(p.Stdout())
	err = p.Wait()
	return strings.TrimSpace(string(b)), err
}

func (s *scripted) Interrupt(context.Context) error {
	select {
	case s.interrupt <- struct{}{}:
	default:
	}
	return nil
}

func (s *scripted) Fork(_ context.Context, f agent.ForkSpec) (string, error) {
	s.mu.Lock()
	replied := s.replied
	s.mu.Unlock()
	if !replied {
		return "", agent.ErrNoFork
	}
	return "fork: " + f.Prompt, nil
}

func (s *scripted) Events() <-chan agent.Event { return s.ev }

func (s *scripted) Answer(_ context.Context, id string, d agent.Decision) error {
	s.mu.Lock()
	ch := s.answers[id]
	delete(s.answers, id)
	s.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("nothing asked %s", id)
	}
	ch <- d
	return nil
}

func (s *scripted) Terminal() agent.Terminal {
	if s.term == nil {
		return nil
	}
	return s.term
}

func (s *scripted) Close() error {
	if s.term != nil {
		s.term.pty.Close()
		<-s.term.drained
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.ev)
	}
	return nil
}

// ptyTerminal is a PTY as views watch it.
type ptyTerminal struct {
	pty     agent.PTY
	drained chan struct{}

	mu      sync.Mutex
	shown   []byte
	viewers map[chan []byte]struct{}
}

func (t *ptyTerminal) drain() {
	defer close(t.drained)
	b := make([]byte, 4096)
	for {
		n, err := t.pty.Read(b)
		if n > 0 {
			chunk := append([]byte(nil), b[:n]...)
			t.mu.Lock()
			t.shown = append(t.shown, chunk...)
			for ch := range t.viewers {
				select {
				case ch <- chunk:
				default:
				}
			}
			t.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	t.pty.Wait()
	t.mu.Lock()
	for ch := range t.viewers {
		close(ch)
	}
	t.viewers = nil
	t.mu.Unlock()
}

func (t *ptyTerminal) Write(p []byte) (int, error) { return t.pty.Write(p) }
func (t *ptyTerminal) Resize(cols, rows int) error { return t.pty.Resize(cols, rows) }

func (t *ptyTerminal) Attach() ([]byte, <-chan []byte, func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ch := make(chan []byte, 64)
	backlog := append([]byte(nil), t.shown...)
	if t.viewers == nil {
		close(ch)
		return backlog, ch, func() {}
	}
	t.viewers[ch] = struct{}{}
	return backlog, ch, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if _, ok := t.viewers[ch]; ok {
			delete(t.viewers, ch)
			close(ch)
		}
	}
}
