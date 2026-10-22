package rpcagent

import "github.com/liamg/oriel/registry"

// Bridge is a plugin's process that serves the protocol for providers configured by others: the ACP bridge runs
// any ACP agent. A provider that names it is served by a process of the bridge's, handed the provider's config in
// initialize.
type Bridge struct {
	Name   string // what a provider's spec names it by: "acp"
	Title  string
	Dial   Dialer // starts a process of the bridge's
	Source string
}

func (Bridge) ContributionKind() string     { return "agent.bridge" }
func (b Bridge) ContributionName() string   { return b.Name }
func (b Bridge) ContributionSource() string { return b.Source }

// BridgeOf is the bridge registered as name.
func BridgeOf(r registry.Contributions, name string) (Bridge, bool) {
	c, ok := r.Contribution("agent.bridge", name)
	if !ok {
		return Bridge{}, false
	}
	b, ok := c.(Bridge)
	return b, ok && b.Dial != nil
}
