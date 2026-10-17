package agenttest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
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
	Account string            // who it's signed in as
	Offers  []agent.Model     // the models it offers; one, "m1", if nil
	Started chan *FakeSession // each session as it starts
	Launch  bool              // a session starts a process through the spec's Exec first, as a real provider's does

	mu        sync.Mutex
	resumed   []string
	signedOut bool
	logins    int // how many times it has signed in: its login's mark
	refuse    error
}

func NewFake() *Fake { return &Fake{Started: make(chan *FakeSession, 16)} }

func (f *Fake) ID() string {
	if f.Name == "" {
		return "fake"
	}
	return f.Name
}

func (f *Fake) Detect(context.Context) (agent.Install, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	in := agent.Install{Bin: "fake", Version: "1", LoggedIn: true, Login: fmt.Sprintf("login-%d", f.logins)}
	if f.signedOut {
		in.LoggedIn, in.Login, in.Note = false, "", "Not signed in: run fake and /login."
	}
	return in, nil
}

// SignIn signs the fake in, afresh, or out, as Detect reports it.
func (f *Fake) SignIn(in bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signedOut = !in
	if in {
		f.logins++
	}
}

func (f *Fake) SignedOut() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.signedOut
}

func (f *Fake) Models(context.Context) ([]agent.Model, error) {
	if f.Offers != nil {
		return slices.Clone(f.Offers), nil
	}
	return []agent.Model{{ID: "m1", Name: "Model One", Efforts: []string{"low", "high"}, Default: true}}, nil
}

func (f *Fake) Usage(context.Context) (core.ProviderUsage, error) {
	return core.ProviderUsage{Provider: f.ID(), Account: f.Account}, nil
}

func (f *Fake) SandboxCredentials() []agent.Mount { return nil }

// Refuse makes Start and Resume fail with err while it isn't nil.
func (f *Fake) Refuse(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refuse = err
}

func (f *Fake) refused() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refuse
}

func (f *Fake) Start(ctx context.Context, s agent.StartSpec) (agent.Session, error) {
	if err := f.refused(); err != nil {
		return nil, err
	}
	if err := f.launch(ctx, s); err != nil {
		return nil, err
	}
	return f.open(s, ""), nil
}

func (f *Fake) Resume(ctx context.Context, id string, s agent.StartSpec) (agent.Session, error) {
	if err := f.refused(); err != nil {
		return nil, err
	}
	if err := f.launch(ctx, s); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.resumed = append(f.resumed, id)
	f.mu.Unlock()
	return f.open(s, id), nil
}

// launch starts the agent's process through the spec's Exec, when the fake is told to.
func (f *Fake) launch(ctx context.Context, s agent.StartSpec) error {
	if !f.Launch || s.Exec == nil {
		return nil
	}
	p, err := s.Exec.Start(ctx, exec.Command(f.ID()))
	if err != nil {
		return err
	}
	go func() { _ = p.Wait() }()
	return nil
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
		answers: map[string]chan agent.Decision{}, fake: f}
	fs.term = &FakeTerminal{s: fs, shown: []byte("fake " + id + "\r\n")}
	fs.ev <- agent.Ready{ID: id}
	f.Started <- fs
	return fs
}

// FakeSession is a session that says what the test tells it to.
type FakeSession struct {
	Spec agent.StartSpec
	Sent chan agent.Input // what it was given, in order

	id          string
	fake        *Fake
	term        *FakeTerminal
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

func (s *FakeSession) Terminal() agent.Terminal { return s.term }

// Term is its terminal, as a test reads it.
func (s *FakeSession) Term() *FakeTerminal { return s.term }

// LoseLogin signs the fake out and says so, as a provider does when its login stops working.
func (s *FakeSession) LoseLogin(detail string) {
	s.fake.SignIn(false)
	s.Emit(agent.AuthExpired{Provider: s.fake.ID(), Account: s.fake.Account, Detail: detail, Terminal: "/login", Shell: "fake, then /login"})
}

// RevokeLogin says the login stopped working while its files still hold it, as a provider does when its token is
// revoked: Detect still reports it signed in, with the same login.
func (s *FakeSession) RevokeLogin(detail string) {
	s.Emit(agent.AuthExpired{Provider: s.fake.ID(), Account: s.fake.Account, Detail: detail, Terminal: "/login", Shell: "fake, then /login"})
}

// FakeTerminal is a fake session's terminal: it shows what's typed, and /login and Enter sign the fake back in.
type FakeTerminal struct {
	s *FakeSession

	mu      sync.Mutex
	shown   []byte
	typed   []byte
	viewers []chan []byte
}

func (t *FakeTerminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	t.typed = append(t.typed, p...)
	t.shown = append(t.shown, p...)
	login := strings.Contains(string(t.typed), "/login\r")
	if login {
		t.typed = nil
	}
	for _, ch := range t.viewers {
		select {
		case ch <- append([]byte(nil), p...):
		default:
		}
	}
	t.mu.Unlock()
	if login {
		t.s.fake.SignIn(true)
		t.s.Emit(agent.AuthRestored{Provider: t.s.fake.ID(), Account: t.s.fake.Account})
	}
	return len(p), nil
}

func (t *FakeTerminal) Resize(int, int) error { return nil }

func (t *FakeTerminal) Attach() ([]byte, <-chan []byte, func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ch := make(chan []byte, 64)
	t.viewers = append(t.viewers, ch)
	return append([]byte(nil), t.shown...), ch, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if i := slices.Index(t.viewers, ch); i >= 0 {
			t.viewers = slices.Delete(t.viewers, i, i+1)
		}
	}
}

// Shown is everything it has shown.
func (t *FakeTerminal) Shown() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.shown)
}

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
	out, err := s.CallErr(name, args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

// CallErr runs one of the tools it was offered, and says how the call went.
func (s *FakeSession) CallErr(name string, args any) (string, error) {
	b, _ := json.Marshal(args)
	for _, tool := range s.Spec.Tools {
		if tool.Name == name {
			return tool.Call(context.Background(), b)
		}
	}
	return "", fmt.Errorf("no tool %s", name)
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
