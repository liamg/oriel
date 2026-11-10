package host

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
)

func loginCard(se core.SessionState) (core.Ask, bool) {
	for _, a := range se.Asks {
		if a.Kind == core.AskLogin {
			return a, true
		}
	}
	return core.Ask{}, false
}

func flagged(se core.SessionState) bool {
	_, ok := loginCard(se)
	return ok && se.Status == core.StatusNeedsYou
}

// A session whose login goes flags every session on the machine and account: needs you, with the
// login's card; the host says it's signed out; a login clears every flag and the sessions go on.
func TestALostLoginFlagsEverySession(t *testing.T) {
	r := newRig(t)
	r.fake.Account = "u1"
	id1, a1 := r.workSession("first")
	a1.WaitSent(t)
	id2, a2 := r.workSession("second")
	a2.WaitSent(t)
	a2.Emit(agent.TurnStart{})
	answered(r, id2, a2)
	a2.Emit(agent.TurnEnd{})
	r.eventually(id2, "the second's turn to end", func(se core.SessionState) bool { return se.Agent.Stats.Turns == 1 })
	a1.Emit(agent.TurnStart{})
	r.eventually(id1, "the first's turn", func(se core.SessionState) bool { return se.Agent.Working })
	a1.LoseLogin("OAuth token revoked · Please run /login")
	a1.Emit(agent.TurnEnd{})

	want := core.Login{Provider: "fake", Account: "u1", Detail: "OAuth token revoked · Please run /login",
		Terminal: "/login", Shell: "fake, then /login"}
	for _, id := range []core.SessionID{id1, id2} {
		se := r.eventually(id, "the login's card", flagged)
		if a, _ := loginCard(se); a.Login == nil || !a.Login.Same(want) {
			t.Errorf("%s: ask = %+v, want %+v", id, a, want)
		}
	}
	if ms := r.w.store.State().Machine; len(ms.SignedOut) != 1 || !ms.SignedOut[0].Same(want) {
		t.Errorf("the machine says %+v", ms.SignedOut)
	}
	if p := r.w.ProviderInfo(context.Background()); p[0].Install.LoggedIn || !strings.Contains(p[0].Install.Note, "OAuth token revoked") {
		t.Errorf("the launcher's provider = %+v", p[0].Install)
	}
	if _, err := r.w.Comment(context.Background(), CommentReq{Session: id2, Body: "and this"}); err != nil {
		t.Fatal(err)
	}
	r.noneSent(a2)

	if _, err := a1.Term().Write([]byte("/login\r")); err != nil {
		t.Fatal(err)
	}
	for _, id := range []core.SessionID{id1, id2} {
		r.eventually(id, "the card to go", func(se core.SessionState) bool { _, ok := loginCard(se); return !ok })
	}
	if ms := r.w.store.State().Machine; len(ms.SignedOut) != 0 {
		t.Errorf("signed in, the machine still says %+v", ms.SignedOut)
	}
	if p := r.w.ProviderInfo(context.Background()); !p[0].Install.LoggedIn {
		t.Errorf("signed in, the launcher's provider = %+v", p[0].Install)
	}
	if got := a1.WaitSent(t); !strings.Contains(got.Text, "signed in again") {
		t.Errorf("the agent whose turn it stopped was told %q", got.Text)
	}
	if got := a2.WaitSent(t); !strings.Contains(got.Text, "and this") {
		t.Errorf("the held comment went as %q", got.Text)
	}
}

// The card's Check again keeps it while the provider is still signed out, and clears it once it isn't.
func TestCheckingAgainKeepsTheCardUntilSignedIn(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("x")
	a.WaitSent(t)
	a.LoseLogin("Not logged in · Run /login")
	first, _ := loginCard(r.eventually(id, "the login's card", flagged))
	if err := r.w.Answer(AnswerReq{Session: id, Ask: first.ID, Decision: "recheck"}); err != nil {
		t.Fatal(err)
	}
	again, _ := loginCard(r.eventually(id, "a card again", func(se core.SessionState) bool {
		a, ok := loginCard(se)
		return ok && a.ID != first.ID
	}))
	r.fake.SignIn(true)
	if err := r.w.Answer(AnswerReq{Session: id, Ask: again.ID, Decision: "recheck"}); err != nil {
		t.Fatal(err)
	}
	r.eventually(id, "the card to go", func(se core.SessionState) bool { _, ok := loginCard(se); return !ok })
	r.signedIn()
}

// signedIn waits for the host to say every login is back: the card goes as it's answered, a moment before.
func (r *rig) signedIn() {
	r.t.Helper()
	until(r.t, "every login back", func() bool { return len(r.w.store.State().Machine.SignedOut) == 0 },
		func() string { return fmt.Sprintf("the machine still says %+v", r.w.store.State().Machine.SignedOut) })
}

// An agent that can't start for want of a login (Codex's) flags its session, and starts once signed in.
func TestAnAgentRefusedForItsLoginStartsOnceSignedIn(t *testing.T) {
	r := newRig(t)
	r.fake.SignIn(false)
	r.fake.Refuse(agent.AuthExpired{Provider: "fake", Detail: "fake isn't signed in", Shell: "fake login"})
	id, _ := r.w.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Prompt: "go on", Provider: "fake",
		Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	if id == "" {
		t.Fatal("no session")
	}
	ask, _ := loginCard(r.eventually(id, "the login's card", flagged))
	if ask.Login.Shell != "fake login" || ask.Login.Terminal != "" {
		t.Errorf("login = %+v", ask.Login)
	}
	r.fake.Refuse(nil)
	r.fake.SignIn(true)
	if err := r.w.Answer(AnswerReq{Session: id, Ask: ask.ID, Decision: "recheck"}); err != nil {
		t.Fatal(err)
	}
	if got := r.agent().WaitSent(t); !strings.Contains(got.Text, "go on") {
		t.Errorf("once signed in, the agent got %q", got.Text)
	}
	r.eventually(id, "the card to go", func(se core.SessionState) bool { _, ok := loginCard(se); return !ok && se.Agent.Running })
}

// Check again with a revoked token on disk stays signed out against a fake provider. The provider's
// files still hold the login the API refused, so it stays signed out until there's a new one, or one is seen to work.
func TestARevokedLoginStaysSignedOutUntilANewOne(t *testing.T) {
	r := newRig(t)
	r.fake.Account = "u1"
	id, a := r.workSession("x")
	a.WaitSent(t)
	a.RevokeLogin("OAuth token revoked · Please run /login")
	card, _ := loginCard(r.eventually(id, "the login's card", flagged))
	if in, _ := r.fake.Detect(context.Background()); !in.LoggedIn {
		t.Fatal("the fake's files should still say signed in")
	}
	if err := r.w.Answer(AnswerReq{Session: id, Ask: card.ID, Decision: "recheck"}); err != nil {
		t.Fatal(err)
	}
	again, _ := loginCard(r.eventually(id, "a card again: the same login is still refused", func(se core.SessionState) bool {
		a, ok := loginCard(se)
		return ok && a.ID != card.ID
	}))
	if p := r.w.ProviderInfo(context.Background()); p[0].Install.LoggedIn {
		t.Errorf("rechecked, the launcher's provider = %+v", p[0].Install)
	}
	r.fake.SignIn(true) // signed in again outside Oriel: a new login on disk
	if err := r.w.Answer(AnswerReq{Session: id, Ask: again.ID, Decision: "recheck"}); err != nil {
		t.Fatal(err)
	}
	r.eventually(id, "the card to go", func(se core.SessionState) bool { _, ok := loginCard(se); return !ok })
	r.signedIn()

	// A login seen to work clears it whatever is on disk.
	a.RevokeLogin("OAuth token revoked · Please run /login")
	r.eventually(id, "the login's card", flagged)
	a.Emit(agent.AuthRestored{Provider: "fake", Account: "u1"})
	r.eventually(id, "the card to go once a login worked", func(se core.SessionState) bool { _, ok := loginCard(se); return !ok })

	// Even when the provider's files say signed out, as a sandbox's /login leaves the host's.
	a.RevokeLogin("OAuth token revoked · Please run /login")
	r.eventually(id, "the login's card", flagged)
	r.fake.SignIn(false)
	a.Emit(agent.AuthRestored{Provider: "fake", Account: "u1"})
	r.eventually(id, "the card to go, signed out on disk", func(se core.SessionState) bool { _, ok := loginCard(se); return !ok })
}

// detectPanics is a provider whose first Detect panics.
type detectPanics struct {
	*agenttest.Fake
	calls atomic.Int32
}

func (p *detectPanics) Detect(ctx context.Context) (agent.Install, error) {
	if p.calls.Add(1) == 1 {
		panic("detect blew up")
	}
	return p.Fake.Detect(ctx)
}

// A check of a lost login doesn't wait forever on reading which login was refused, when that read failed.
func TestALoginCheckOutlivesAFailedMark(t *testing.T) {
	p := &detectPanics{Fake: &agenttest.Fake{Name: "flaky"}}
	r := newRig(t, p)
	r.w.run.LoginLost("", agent.AuthExpired{Provider: "flaky", Account: "u1", Detail: "gone"})
	until(t, "the login's read", func() bool { return p.calls.Load() > 0 })
	done := make(chan bool)
	go func() { done <- r.w.run.CheckLogin("flaky", false) }()
	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Fatal("checkLogin is still waiting")
	}
}

// A provider that signs in from Oriel says so on its card; StartLogin opens its sign-in, and once it succeeds every
// card goes and the sessions go on.
func TestABrowserLoginClearsTheCards(t *testing.T) {
	r := newRig(t)
	r.fake.Has = &agent.Features{Login: true}
	id, a := r.workSession("x")
	a.WaitSent(t)
	r.fake.SignIn(false)
	a.Emit(agent.AuthExpired{Provider: "fake", Detail: "fake is signed out", Shell: "fake login"})
	ask, _ := loginCard(r.eventually(id, "the login's card", flagged))
	if !ask.Login.Browser {
		t.Errorf("login = %+v, want it signed in from Oriel", ask.Login)
	}
	if p := r.w.ProviderInfo(context.Background()); !p[0].Has.Login || p[0].Has.Terminal {
		t.Errorf("the provider has %+v", p[0].Has)
	}
	url, err := r.w.StartLogin(context.Background(), "fake")
	if err != nil || url != agenttest.LoginURL {
		t.Fatalf("StartLogin = %q, %v", url, err)
	}
	r.fake.FinishLogin(nil)
	r.eventually(id, "the card to go", func(se core.SessionState) bool { _, ok := loginCard(se); return !ok })
	r.signedIn()
}

// A provider that doesn't sign in from Oriel can't be asked to.
func TestStartLoginNeedsALoginStarter(t *testing.T) {
	r := newRig(t)
	if _, err := r.w.StartLogin(context.Background(), "fake"); err == nil {
		t.Error("a provider without Login signed in from Oriel")
	}
	if _, err := r.w.StartLogin(context.Background(), "nobody"); err == nil {
		t.Error("no provider signed in")
	}
}

// Quitting doesn't wait on a sign-in nobody finished: the host closes at once, the sign-in left where it was.
func TestCloseDoesNotWaitForASignInLeftOpen(t *testing.T) {
	r := newRig(t)
	r.fake.Has = &agent.Features{Login: true}
	if _, err := r.w.StartLogin(context.Background(), "fake"); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- r.w.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(waitTimeout): // a hang, not a wait: Close returns at once
		t.Fatal("Close is still waiting for the sign-in")
	}
}

// Whatever the host still has running when it closes, closing waits for it only so long, then says what it left.
func TestCloseWaitsOnlySoLongAndSaysForWhat(t *testing.T) {
	r := newRig(t)
	var logged strings.Builder
	r.w.log = slog.New(slog.NewTextHandler(&logged, nil))
	r.w.opts.CloseWait = time.Millisecond
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	r.w.env.Go("a stuck fetch", func() { <-stuck })
	if err := r.w.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logged.String(), "a stuck fetch") {
		t.Errorf("closing didn't say what it left running: %q", logged.String())
	}
}
