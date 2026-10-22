package registry

import (
	"cmp"
	"slices"
	"strings"

	"github.com/liamg/oriel/agent"
)

// AgentProvider is a kind of agent sessions run (Claude, Codex, a plugin's), as its plugin brings it. The session
// host starts it by Name, which is its Provider's ID; one registered later under the same name shadows it.
type AgentProvider struct {
	Name     string // "claude": the Provider's ID
	Title    string // "Claude"
	Mark     string // its square's letters: "C"; its initials if ""
	Tone     string // its square's theme token; muted if ""
	Order    int    // the first installed in Order is the default
	Provider agent.Provider
	Source   string
}

func (AgentProvider) ContributionKind() string     { return "agent.provider" }
func (p AgentProvider) ContributionName() string   { return p.Name }
func (p AgentProvider) ContributionSource() string { return p.Source }

// AgentProviders are the providers registered, by Order, then name.
func AgentProviders(r Contributions) []AgentProvider {
	var out []AgentProvider
	for _, c := range r.Contributions("agent.provider") {
		if p, ok := c.(AgentProvider); ok && p.Provider != nil {
			out = append(out, p)
		}
	}
	slices.SortStableFunc(out, func(a, b AgentProvider) int { return cmp.Compare(a.Order, b.Order) })
	return out
}

// ProviderTitle is what a provider is called: its registered Title, else its name capitalised; "Agent" for none.
func ProviderTitle(r Contributions, name string) string {
	if name == "" {
		return "Agent"
	}
	if r != nil {
		if c, ok := r.Contribution("agent.provider", name); ok {
			if p, ok := c.(AgentProvider); ok && p.Title != "" {
				return p.Title
			}
		}
	}
	return strings.ToUpper(name[:1]) + name[1:]
}
