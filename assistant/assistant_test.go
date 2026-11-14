package assistant

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/internal/clock/clocktest"
)

// fakeAgent is an agent the test speaks for.
type fakeAgent struct {
	sig    chan Signal
	sent   chan string
	poke   func() // told when it closes
	mu     sync.Mutex
	closed bool
	stops  int
}

func newFake() *fakeAgent { return &fakeAgent{sig: make(chan Signal, 64), sent: make(chan string, 16)} }

func (f *fakeAgent) Send(_ context.Context, text string) error { f.sent <- text; return nil }
func (f *fakeAgent) Interrupt(context.Context) error {
	f.mu.Lock()
	f.stops++
	f.mu.Unlock()
	f.sig <- Signal{Kind: TurnEnded}
	return nil
}
func (f *fakeAgent) Signals() <-chan Signal { return f.sig }
func (f *fakeAgent) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.sig)
		if f.poke != nil {
			f.poke()
		}
	}
	return nil
}
func (f *fakeAgent) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// rig is a conversation whose agents are fakes, started as the test asks, on a clock the test moves.
type rig struct {
	t       *testing.T
	c       *Conversation
	clock   *clocktest.Fake
	starts  chan string
	agents  chan *fakeAgent
	fail    error
	changed chan struct{} // the log changed, or an agent closed
}

func newRig(t *testing.T) *rig {
	r := &rig{t: t, starts: make(chan string, 4), agents: make(chan *fakeAgent, 4), changed: make(chan struct{}, 1),
		clock: clocktest.New(time.Date(2026, 10, 15, 14, 5, 0, 0, time.UTC))}
	r.c = New(func(_ context.Context, first string) (Agent, error) {
		if r.fail != nil {
			return nil, r.fail
		}
		r.starts <- first
		a := newFake()
		a.poke = r.poke
		r.agents <- a
		return a, nil
	})
	r.c.Changed = r.poke
	r.c.Clock = r.clock
	t.Cleanup(r.c.Close)
	return r
}

func (r *rig) started() (string, *fakeAgent) {
	r.t.Helper()
	select {
	case first := <-r.starts:
		return first, <-r.agents
	case <-time.After(5 * time.Second):
		r.t.Fatal("no agent started")
	}
	return "", nil
}

func (r *rig) poke() {
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

// until waits for ok to hold, looking again each time the log changes or an agent closes.
func (r *rig) until(what string, ok func([]Entry, bool) bool) []Entry {
	r.t.Helper()
	hung := time.After(10 * time.Second)
	for {
		log, busy := r.c.Log()
		if ok(log, busy) {
			return log
		}
		select {
		case <-r.changed:
		case <-hung:
			r.t.Fatalf("waiting for %s: %+v", what, log)
		}
	}
}

func kinds(log []Entry) string {
	var out []string
	for _, e := range log {
		out = append(out, string(e.Kind))
	}
	return strings.Join(out, " ")
}

// crit's assistant: one agent per conversation, started on its first turn; turns are stamped with when and how they
// were said; a turn waits while one runs; the reply tool's answer is the reply, else the agent's last words.
func TestAConversation(t *testing.T) {
	r := newRig(t)
	r.c.Progress = func(tool string) string {
		if tool == "sessions" {
			return "checking your sessions…"
		}
		return ""
	}
	r.c.Say("what needs me?", false)
	r.c.Say("  ", false)
	first, a := r.started()
	if first != "[Thu 15 Oct 14:05, typed] what needs me?" {
		t.Errorf("first turn %q", first)
	}
	r.c.Say("and my PRs?", true)
	a.sig <- Signal{Kind: TurnStarted}
	a.sig <- Signal{Kind: UsedTool, Tool: "sessions"}
	r.until("the tool", func(l []Entry, _ bool) bool { return strings.Contains(kinds(l), "tool progress") })
	select {
	case s := <-a.sent:
		t.Fatalf("a second turn went in while the first ran: %q", s)
	default:
	}
	r.c.Reply("Four things, oldest first.", false, []Item{{Title: "review #4410", Session: "rv-4410"}})
	r.c.Reply("again", false, nil)
	a.sig <- Signal{Kind: Said, Text: "ignored: the reply tool answered"}
	a.sig <- Signal{Kind: TurnEnded}
	if s := <-a.sent; s != "[Thu 15 Oct 14:05, spoken] and my PRs?" {
		t.Errorf("second turn %q", s)
	}
	if !r.c.Voice() {
		t.Error("the spoken turn isn't the voice turn")
	}
	a.sig <- Signal{Kind: Said, Text: "None open."}
	a.sig <- Signal{Kind: TurnEnded}
	log := r.until("both replies", func(l []Entry, busy bool) bool { return !busy && strings.Count(kinds(l), "reply") == 2 })
	if got := kinds(log); got != "user user tool progress reply reply" {
		t.Fatalf("log: %s", got)
	}
	if log[4].Text != "Four things, oldest first." || len(log[4].Items) != 1 || log[4].Voice {
		t.Errorf("first reply %+v", log[4])
	}
	if log[5].Text != "None open." || !log[5].Voice {
		t.Errorf("second reply %+v", log[5])
	}
}

// Confirmation cards: an act waits for the user's yes, no, or the wait running out; the answer is on its entry.
func TestConfirmations(t *testing.T) {
	r := newRig(t)
	r.c.ConfirmWait = time.Minute
	done := make(chan Answer, 1)
	go func() {
		done <- r.c.Confirm(context.Background(), "review_pr", "start a review of acme/api#4410", "card")
	}()
	w := r.until("the card", func(l []Entry, _ bool) bool { return len(l) == 1 })
	if w[0].Kind != KindConfirm || w[0].Card != "card" || w[0].Tool != "review_pr" || w[0].Until.IsZero() {
		t.Fatalf("card: %+v", w[0])
	}
	if len(r.c.Waiting()) != 1 {
		t.Fatal("not waiting")
	}
	if r.c.Answered("nope", true) {
		t.Error("an unknown card was answered")
	}
	if !r.c.Answered(w[0].ID, true) {
		t.Fatal("the answer didn't land")
	}
	if a := <-done; a != Yes {
		t.Errorf("answer %q", a)
	}
	if r.c.Answered(w[0].ID, false) {
		t.Error("answered twice")
	}
	go func() { done <- r.c.Confirm(context.Background(), "stop_session", "stop rounding", nil) }()
	r.until("the second card", func(l []Entry, _ bool) bool { return len(l) == 2 })
	r.clock.Advance(time.Minute - time.Nanosecond)
	select {
	case a := <-done:
		t.Fatalf("answered %q before the wait ran out", a)
	default:
	}
	r.clock.Advance(time.Nanosecond)
	if a := <-done; a != NoAnswer {
		t.Errorf("unanswered: %q", a)
	}
	log := r.until("the answers", func(l []Entry, _ bool) bool { return len(l) == 2 && l[1].Answer != "" })
	if log[0].Answer != Yes || log[1].Answer != NoAnswer || len(r.c.Waiting()) != 0 {
		t.Errorf("answers: %+v", log)
	}
}

// Every action has an undo with a countdown.
func TestActionsCanBeUndoneForAWhile(t *testing.T) {
	r := newRig(t)
	now := r.clock.Now()
	undone := 0
	e := r.c.Acted(Act{Tool: "review_pr", Line: "started a review of acme/api#4410", Open: "session.open", OpenArgs: map[string]any{"id": "rv"},
		Undo: func(context.Context) error { undone++; return nil }})
	if e.Until != now.Add(r.c.UndoWait) {
		t.Errorf("undo until %v", e.Until)
	}
	if err := r.c.Undo(context.Background(), e.ID); err != nil || undone != 1 {
		t.Fatalf("undo: %v", err)
	}
	if err := r.c.Undo(context.Background(), e.ID); err == nil {
		t.Error("undone twice")
	}
	late := r.c.Acted(Act{Line: "renamed", Undo: func(context.Context) error { undone++; return nil }})
	r.clock.Advance(time.Minute)
	if err := r.c.Undo(context.Background(), late.ID); err == nil || undone != 1 {
		t.Error("undone after its countdown")
	}
	plain := r.c.Acted(Act{Line: "opened rounding"})
	if !plain.Until.IsZero() || r.c.Undo(context.Background(), plain.ID) == nil {
		t.Error("an action with no undo offered one")
	}
	log, _ := r.c.Log()
	if !log[0].Undone || log[1].Undone {
		t.Errorf("undone marks: %+v", log)
	}
}

// A conversation ends when cleared or left idle; the next turn starts a fresh agent. An agent that can't start, or
// stops mid-turn, says why.
func TestFreshConversations(t *testing.T) {
	r := newRig(t)
	r.c.Say("hi", false)
	_, a := r.started()
	a.sig <- Signal{Kind: Said, Text: "Hello."}
	a.sig <- Signal{Kind: TurnEnded}
	r.until("the reply", func(l []Entry, busy bool) bool { return !busy && len(l) == 2 })
	r.c.Clear()
	r.until("the old agent closed", func([]Entry, bool) bool { return a.isClosed() })
	r.c.Say("again", false)
	_, b := r.started()
	if b == a {
		t.Fatal("the same agent")
	}
	b.sig <- Signal{Kind: TurnEnded}
	r.until("idle", func(_ []Entry, busy bool) bool { return !busy })
	r.c.Idle = 0
	r.c.ReapIdle()
	r.until("reaped", func([]Entry, bool) bool { return b.isClosed() })
	log, _ := r.c.Log()
	if got := kinds(log); got != "user reply divider user divider" {
		t.Errorf("log: %s", got)
	}

	r.c.Say("mid-turn", false)
	_, d := r.started()
	d.Close()
	r.until("why it stopped", func(l []Entry, busy bool) bool { return !busy && l[len(l)-1].Kind == KindError })

	r.fail = errors.New("claude is signed out")
	r.c.Say("hello?", false)
	log = r.until("the start's error", func(l []Entry, busy bool) bool {
		return !busy && strings.Contains(l[len(l)-1].Text, "claude is signed out")
	})
	if log[len(log)-1].Kind != KindError {
		t.Errorf("%+v", log[len(log)-1])
	}
}

func TestInterrupt(t *testing.T) {
	r := newRig(t)
	r.c.Say("a long one", false)
	_, a := r.started()
	r.until("busy", func(_ []Entry, busy bool) bool { return busy })
	if err := r.c.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.until("stopped", func(_ []Entry, busy bool) bool { return !busy })
	if a.stops != 1 {
		t.Errorf("interrupts: %d", a.stops)
	}
}

// An idle conversation ends at its deadline, from a timer set to it rather than a once-a-second check.
func TestIdleConversationEndsOnTime(t *testing.T) {
	r := newRig(t)
	r.c.Idle = 100 * time.Millisecond
	r.c.Say("hi", false)
	_, a := r.started()
	a.sig <- Signal{Kind: TurnEnded}
	r.until("idle", func(_ []Entry, busy bool) bool { return !busy })
	r.wantEndedAfter(100*time.Millisecond, a)
}

// wantEndedAfter checks the conversation ends exactly d from now, closing a.
func (r *rig) wantEndedAfter(d time.Duration, a *fakeAgent) {
	r.t.Helper()
	ended := func() bool { l, _ := r.c.Log(); return len(l) > 0 && l[len(l)-1].Kind == KindDivider }
	r.clock.Advance(d - time.Nanosecond)
	if ended() {
		r.t.Fatalf("ended before %v", d)
	}
	r.clock.Advance(time.Nanosecond)
	if !ended() {
		r.t.Fatalf("not ended at %v", d)
	}
	r.until("reaped", func([]Entry, bool) bool { return a.isClosed() })
}

// Shortening Idle while a conversation waits re-arms its reaper to the new deadline, rather than the old one.
func TestShorterIdleRearmsTheReaper(t *testing.T) {
	r := newRig(t)
	r.c.SetIdle(time.Hour)
	r.c.Say("hi", false)
	_, a := r.started()
	a.sig <- Signal{Kind: TurnEnded}
	r.until("idle", func(_ []Entry, busy bool) bool { return !busy })
	r.c.SetIdle(100 * time.Millisecond)
	r.wantEndedAfter(100*time.Millisecond, a)
}
