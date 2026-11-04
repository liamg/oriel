package host

import (
	"slices"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
)

// A sandboxed agent's sign-in is a copy of the host's, which the host may have renewed since: one it says is gone is
// handed the host's again and started again, with no card. Gone again, the card asks, and signs in on the host:
// never in the sandbox's own terminal, where a sign-in would be the sandbox's alone.
func TestASandboxedAgentsLostSignInIsTriedAgainFromTheHost(t *testing.T) {
	s := newSandboxRig(t)
	id := s.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Work", Provider: "fake", Repos: []core.RepoID{core.RepoID(s.repo.Dir)},
		Sandbox: core.SandboxPolicy{Container: true, Network: "ask"}})
	a := s.agent()
	a.WaitSent(t)
	a.RevokeLogin("OAuth token revoked · Please run /login")
	again := s.agent() // started again
	if se := s.session(id); func() bool { _, ok := loginCard(se); return ok }() {
		t.Fatalf("the first lost sign-in raised a card: %+v", se.Asks)
	}
	again.RevokeLogin("OAuth token revoked · Please run /login")
	se := s.eventually(id, "the login's card", flagged)
	card, _ := loginCard(se)
	if card.Login == nil || card.Login.Terminal != "" {
		t.Errorf("the card signs in through the sandbox's terminal: %+v", card.Login)
	}
}

// A provider whose sandboxes can't sign in because the host isn't signed in raises the sign-in card, like a lost
// login: not a launch error. It signs in on the host with the provider's command.
func TestASandboxThatNeedsTheHostsSignInAsksForIt(t *testing.T) {
	p := &boxed{Fake: agenttest.NewFake(), at: make(chan agent.SandboxPlace, 4),
		needs: agent.SandboxNeeds{Problems: []agent.SandboxProblem{{Code: "boxed-login-missing", Blocking: true,
			Message: "Boxed isn't signed in on this machine", Fix: "Run `boxed login`", SignIn: []string{"boxed", "login"}}}}}
	p.Name, p.Launch = "boxed", true
	s := newSandboxRig(t, p)
	id := s.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Work", Provider: "boxed", Repos: []core.RepoID{core.RepoID(s.repo.Dir)},
		Sandbox: core.SandboxPolicy{Container: true, Network: "ask"}})
	se := s.eventually(id, "the login's card", flagged)
	card, _ := loginCard(se)
	if card.Login == nil || card.Login.Provider != "boxed" || !slices.Equal(card.Login.HostCommand, []string{"boxed", "login"}) {
		t.Errorf("the card = %+v", card.Login)
	}
}
