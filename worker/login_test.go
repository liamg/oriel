package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
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

// AI-13 done when: a session whose login goes flags every session on that worker and account: needs you, with the
// login's card; the worker says it's signed out; a login clears every flag and the sessions go on.
func TestALostLoginFlagsEverySessionOnTheWorker(t *testing.T) {
	r := newRig(t)
	r.fake.Account = "u1"
	id1, a1 := r.workSession("first")
	a1.WaitSent(t)
	id2, a2 := r.workSession("second")
	a2.WaitSent(t)
	a2.Emit(agent.TurnStart{})
	a2.Emit(agent.TurnEnd{})
	r.eventually(id2, "the second's turn to end", func(se core.SessionState) bool { return se.Agent.Stats.Turns == 1 })
	a1.Emit(agent.TurnStart{})
	r.eventually(id1, "the first's turn", func(se core.SessionState) bool { return se.Agent.Working })
	a1.LoseLogin("OAuth token revoked · Please run /login")
	a1.Emit(agent.TurnEnd{})

	want := core.Login{Provider: "fake", Account: "u1", Worker: core.LocalWorker, Detail: "OAuth token revoked · Please run /login",
		Terminal: "/login", Shell: "fake, then /login"}
	for _, id := range []core.SessionID{id1, id2} {
		se := r.eventually(id, "the login's card", flagged)
		if a, _ := loginCard(se); a.Login == nil || *a.Login != want {
			t.Errorf("%s: ask = %+v, want %+v", id, a, want)
		}
	}
	if ws := r.w.workerState(); len(ws.SignedOut) != 1 || ws.SignedOut[0] != want {
		t.Errorf("worker says %+v", ws.SignedOut)
	}
	if p := r.w.ProviderInfo(context.Background()); p[0].Install.LoggedIn || !strings.Contains(p[0].Install.Note, "OAuth token revoked") {
		t.Errorf("the launcher's provider = %+v", p[0].Install)
	}
	if _, err := r.w.Comment(context.Background(), CommentReq{Session: id2, Body: "and this"}); err != nil {
		t.Fatal(err)
	}
	a2.NoneSent(t)

	if _, err := a1.Term().Write([]byte("/login\r")); err != nil {
		t.Fatal(err)
	}
	for _, id := range []core.SessionID{id1, id2} {
		r.eventually(id, "the card to go", func(se core.SessionState) bool { _, ok := loginCard(se); return !ok })
	}
	if ws := r.w.workerState(); len(ws.SignedOut) != 0 {
		t.Errorf("signed in, the worker still says %+v", ws.SignedOut)
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

// AI-13: the card's Check again keeps it while the provider is still signed out, and clears it once it isn't.
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

// signedIn waits for the worker to say every login is back: the card goes as it's answered, a moment before.
func (r *rig) signedIn() {
	r.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); len(r.w.workerState().SignedOut) > 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			r.t.Fatalf("signed in, the worker still says %+v", r.w.workerState().SignedOut)
		}
	}
}

// AI-13: an agent that can't start for want of a login (Codex's) flags its session, and starts once signed in.
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

// SE-5 done when: "Check again with a revoked token on disk stays signed out against a fake provider". The provider's
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
}
