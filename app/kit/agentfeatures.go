package kit

import (
	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/registry"
)

// ProviderFeatures are what provider name's sessions can do (agent.Features), as the plugin that registered it
// (registry.AgentProvider) says; ok is false for a provider the registry doesn't know.
func ProviderFeatures(r registry.Contributions, name string) (agent.Features, bool) {
	if r == nil || name == "" {
		return agent.Features{}, false
	}
	c, ok := r.Contribution("agent.provider", name)
	if !ok {
		return agent.Features{}, false
	}
	p, ok := c.(registry.AgentProvider)
	if !ok || p.Provider == nil {
		return agent.Features{}, false
	}
	return agent.FeaturesOf(p.Provider), true
}
