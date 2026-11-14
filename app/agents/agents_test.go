package agents_test

import (
	"testing"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/agent/claude"
	"github.com/liamg/oriel/app/agents"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
)

// Claude and Codex come in as plugins, each registering its provider as any plugin would: Claude first, each
// called what it is, with its square's letters; Claude's sandbox version is an option of its own, which its
// provider reads.
func TestTheBuiltInProvidersArePlugins(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir(), Config: func(cfg *apptest.Config) { cfg.Agents = agents.Builtins("") }})
	r := s.Registry()
	ps := services.AgentProviders(r)
	if len(ps) != 2 || ps[0].Name != "claude" || ps[0].Title != "Claude" || ps[0].Mark != "C" || ps[1].Name != "codex" ||
		ps[1].Title != "Codex" || ps[1].Mark != "CX" {
		t.Fatalf("providers = %+v", ps)
	}
	if ps[0].Provider.ID() != "claude" || ps[1].Provider.ID() != "codex" {
		t.Errorf("the providers' IDs aren't their names: %s, %s", ps[0].Provider.ID(), ps[1].Provider.ID())
	}
	if err := r.Options().Set(agents.VersionOption, registry.LayerUser, registry.At{}, "2.1.300"); err != nil {
		t.Fatal(err)
	}
	if got := ps[0].Provider.(*claude.Provider).SandboxVersion(); got != "2.1.300" {
		t.Errorf("Claude's sandbox version = %q", got)
	}
}
