package agent_test

import (
	"slices"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
)

func named(name string) *agenttest.Fake {
	f := agenttest.NewFake()
	f.Name = name
	return f
}

func ids(ps []agent.Provider) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.ID())
	}
	return out
}

// The registered providers win their IDs; the source adds the rest, after them.
func TestProvidersSourceAddsWhatIsNotRegistered(t *testing.T) {
	var s agent.Providers
	own := named("claude")
	s.Register(own)
	app := named("claude")
	s.SetSource(func() []agent.Provider { return []agent.Provider{app, named("codex")} })
	if p, _ := s.Get("claude"); p != own {
		t.Errorf("claude is the source's, want the registered one")
	}
	if _, ok := s.Get("codex"); !ok {
		t.Errorf("codex from the source isn't found")
	}
	if got := ids(s.List()); !slices.Equal(got, []string{"claude", "codex"}) {
		t.Errorf("List = %v", got)
	}
	s.SetSource(nil)
	if _, ok := s.Get("codex"); ok {
		t.Errorf("codex is still found with no source")
	}
}
