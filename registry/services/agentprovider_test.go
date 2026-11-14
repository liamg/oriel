package services_test

import (
	"testing"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/registry"
)

// Providers come by Order, a later one shadows its name, and a provider is called what its plugin says.
func TestAgentProvidersByOrderAndTitle(t *testing.T) {
	r := registry.New()
	r.Contribute(services.AgentProvider{Name: "codex", Title: "Codex", Order: 20, Provider: agenttest.NewFake()})
	claude := r.Contribute(services.AgentProvider{Name: "claude", Title: "Claude", Order: 10, Provider: agenttest.NewFake()})
	mine := r.Contribute(services.AgentProvider{Name: "claude", Title: "My Claude", Order: 10, Provider: agenttest.NewFake()})
	ps := services.AgentProviders(r)
	if len(ps) != 2 || ps[0].Name != "claude" || ps[1].Name != "codex" {
		t.Fatalf("providers = %+v", ps)
	}
	if got := services.ProviderTitle(r, "claude"); got != "My Claude" {
		t.Errorf("claude is called %q, want the later plugin's", got)
	}
	mine.Dispose()
	if got := services.ProviderTitle(r, "claude"); got != "Claude" {
		t.Errorf("claude is called %q once the later one is gone", got)
	}
	claude.Dispose()
	for name, want := range map[string]string{"claude": "Claude", "ollama": "Ollama", "": "Agent"} {
		if got := services.ProviderTitle(r, name); got != want {
			t.Errorf("ProviderTitle(%q) = %q, want %q", name, got, want)
		}
	}
}
