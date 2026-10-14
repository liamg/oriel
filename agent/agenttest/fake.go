package agenttest

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

// Fake is a provider for tests of what drives one (the worker, the app): it records what it's given, and its
// sessions say only what the test tells them to.
type Fake struct {
	Name    string            // its ID; "fake" if ""
	Started chan *FakeSession // each session as it starts

	mu      sync.Mutex
	resumed []string
}

func NewFake() *Fake { return &Fake{Started: make(chan *FakeSession, 16)} }

func (f *Fake) ID() string {
	if f.Name == "" {
		return "fake"
	}
	return f.Name
}

func (f *Fake) Detect(context.Context) (agent.Install, error) {
	return agent.Install{Bin: "fake", Version: "1", LoggedIn: true}, nil
}

func (f *Fake) Models(context.Context) ([]agent.Model, error) {
	return []agent.Model{{ID: "m1", Name: "Model One", Efforts: []string{"low", "high"}, Default: true}}, nil
}

func (f *Fake) Usage(context.Context) (core.ProviderUsage, error) {
	return core.ProviderUsage{Provider: f.ID()}, nil
}

func (f *Fake) SandboxCredentials() []agent.Mount { return nil }

func (f *Fake) Start(_ context.Context, s agent.StartSpec) (agent.Session, error) {
	return f.open(s, ""), nil
}

func (f *Fake) Resume(_ context.Context, id string, s agent.StartSpec) (agent.Session, error) {
	f.mu.Lock()
	f.resumed = append(f.resumed, id)
	f.mu.Unlock()
	return f.open(s, id), nil
}

// Resumed are the ids sessions were resumed by.
func (f *Fake) Resumed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.resumed)
}

var fakeSeq struct {
	sync.Mutex
	n int
}

func (f *Fake) open(s agent.StartSpec, id string) *FakeSession {
	if id == "" {
		fakeSeq.Lock()
		fakeSeq.n++
		id = "fake-" + time.Now().Format("150405") + "-" + string(rune('a'+fakeSeq.n%26))
		fakeSeq.Unlock()
	}
	fs := &FakeSession{id: id, Spec: s, ev: make(chan agent.Event, 256), Sent: make(chan agent.Input, 64),
		answers: map[string]chan agent.Decision{}}
	fs.ev <- agent.Ready{ID: id}
	f.Started <- fs
	return fs
}

// FakeSession is a session that says what the test tells it to.
type FakeSession struct {
	Spec agent.StartSpec
	Sent chan agent.Input // what it was given, in order

	id          string
	ev          chan agent.Event
	mu          sync.Mutex
	interrupted int
	closed      bool
	answers     map[string]chan agent.Decision
}

func (s *FakeSession) ID() string { return s.id }

func (s *FakeSession) Send(_ context.Context, in agent.Input) error {
	s.Sent <- in
	return nil
}

// Interrupt ends the turn, as a provider does.
func (s *FakeSession) Interrupt(context.Context) error {
	s.mu.Lock()
	s.interrupted++
	s.mu.Unlock()
	s.Emit(agent.TurnEnd{})
	return nil
}

// Interrupts is how many times it was interrupted.
func (s *FakeSession) Interrupts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.interrupted
}

// Fork answers "fork says: " and the end of the prompt.
func (s *FakeSession) Fork(_ context.Context, f agent.ForkSpec) (string, error) {
	p := f.Prompt
	if len(p) > 40 {
		p = p[len(p)-40:]
	}
	return "fork says: " + p, nil
}

func (s *FakeSession) Events() <-chan agent.Event { return s.ev }

func (s *FakeSession) Answer(_ context.Context, id string, d agent.Decision) error {
	s.mu.Lock()
	ch := s.answers[id]
	s.mu.Unlock()
	if ch == nil {
		return errors.New("nothing asked " + id)
	}
	ch <- d
	return nil
}

func (s *FakeSession) Terminal() agent.Terminal { return nil }

func (s *FakeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.ev)
	}
	return nil
}

// Closed reports whether whoever started it has closed it.
func (s *FakeSession) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Emit says ev, as the agent would.
func (s *FakeSession) Emit(ev agent.Event) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if !closed {
		s.ev <- ev
	}
}

// Expect is where the answer to the permission or question id lands.
func (s *FakeSession) Expect(id string) chan agent.Decision {
	ch := make(chan agent.Decision, 1)
	s.mu.Lock()
	s.answers[id] = ch
	s.mu.Unlock()
	return ch
}

// Ask raises a permission and returns where its answer lands.
func (s *FakeSession) Ask(p agent.Permission) chan agent.Decision {
	ch := s.Expect(p.ID)
	s.Emit(p)
	return ch
}

// Call runs one of the tools it was offered, as the agent would.
func (s *FakeSession) Call(t testing.TB, name string, args any) string {
	t.Helper()
	b, _ := json.Marshal(args)
	for _, tool := range s.Spec.Tools {
		if tool.Name == name {
			out, err := tool.Call(context.Background(), b)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			return out
		}
	}
	t.Fatalf("no tool %s", name)
	return ""
}

// WaitSent is the next thing it was given.
func (s *FakeSession) WaitSent(t testing.TB) agent.Input {
	t.Helper()
	select {
	case in := <-s.Sent:
		return in
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was sent to the agent")
	}
	return agent.Input{}
}

// NoneSent checks it's given nothing for a moment.
func (s *FakeSession) NoneSent(t testing.TB) {
	t.Helper()
	select {
	case in := <-s.Sent:
		t.Fatalf("the agent was sent %q, want nothing yet", in.Text)
	case <-time.After(100 * time.Millisecond):
	}
}

// WaitStarted is the next session the provider starts.
func (f *Fake) WaitStarted(t testing.TB) *FakeSession {
	t.Helper()
	select {
	case s := <-f.Started:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("no agent started")
	}
	return nil
}
