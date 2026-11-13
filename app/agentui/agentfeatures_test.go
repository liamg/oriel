package agentui_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// registers is a plugin that registers the fake as its provider, as a plugin's agent would.
type registers struct{ p agent.Provider }

func (r registers) Manifest() plugin.Manifest { return plugin.Manifest{ID: "test.provider"} }
func (r registers) Activate(_ context.Context, h plugin.Host) error {
	h.Contribute(registry.AgentProvider{Name: r.p.ID(), Title: "Fake", Provider: r.p, Source: "test"})
	return nil
}
func (registers) Deactivate(context.Context) error { return nil }

// An agent whose provider says it has no terminal of its own offers none: no button, no command.
func TestNoRawTerminalWithoutOne(t *testing.T) {
	lw := rig.NewHost(t)
	lw.Fake.Has = &agent.Features{Fork: true}
	r, _, _ := apptest.AgentWindowWith(t, lw, apptest.WindowOpts{Plugins: []plugin.Plugin{registers{lw.Fake}}})
	r.TypeKeys("<D-A>")
	s := r.WaitText("SESSION STATS")
	if strings.Contains(s, "Raw terminal") {
		t.Error("the console offers a raw terminal the agent hasn't got")
	}
	err := r.Do(func() error { return r.Host.Run(context.Background(), "agent.terminal", nil) })
	if err == nil {
		t.Error("agent.terminal ran for an agent with no terminal")
	}
}

// A lost login of a provider that signs in from Oriel: the card's Log in opens its sign-in in the browser, and once
// it succeeds the card goes and the session goes on.
func TestABrowserLoginFromTheCard(t *testing.T) {
	lw := rig.NewHost(t)
	lw.Fake.Has = &agent.Features{Login: true}
	desk := &apptest.Desktop{}
	r, a, _ := apptest.AgentWindowWith(t, lw, apptest.WindowOpts{Config: func(c *apptest.Config) { c.Desktop = desk }})
	lw.Fake.SignIn(false)
	a.Emit(agent.AuthExpired{Provider: "fake", Detail: "fake is signed out", Shell: "fake login"})
	r.WaitFrame("the card", func(s string) bool {
		return apptest.HasAll(s, "Fake needs you to log in", "opens its sign-in in your browser", "Log in", "Check again")
	})
	r.ClickText("Log in")
	apptest.Poll(t, "the sign-in to start", lw.Fake.LoginStarted)
	r.UntilState("the browser opened", func(core.State) bool {
		var opened bool
		r.Do(func() error { opened = slices.Contains(desk.Opened, agenttest.LoginURL); return nil })
		return opened
	})
	lw.Fake.FinishLogin(nil)
	r.WaitGone("needs you to log in")
}

// A login that signs in on the host (a sandboxed agent's, whose sign-in is the host's): the card's Log in runs the
// provider's command in a terminal on this machine.
func TestAHostLoginFromTheCard(t *testing.T) {
	lw := rig.NewHost(t)
	r, a, _ := apptest.AgentWindowWith(t, lw, apptest.WindowOpts{})
	lw.Fake.SignIn(false)
	a.Emit(agent.AuthExpired{Provider: "fake", Detail: "fake is signed out", Shell: "fake login",
		HostCommand: []string{"sh", "-c", "echo signing-in-on-the-host; exec cat"}})
	r.WaitFrame("the card", func(s string) bool {
		return apptest.HasAll(s, "Fake needs you to log in", "signs in on this machine", "Log in", "Check again")
	})
	r.ClickText("Log in")
	r.WaitText("signing-in-on-the-host")
}
