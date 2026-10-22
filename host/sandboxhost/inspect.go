package sandboxhost

import (
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/egress"
	"github.com/liamg/oriel/sandbox/gateway"
)

// What a box runs on and with, as the Permissions and Workspace views read it.

// Root is the workspace the box confines.
func (b *Box) Root() string { return b.root }

// Config is the sandbox settings the box opened with.
func (b *Box) Config() sandbox.Config { return b.cfg }

// Egress is the box's egress session: the network rules in force and the connections made.
func (b *Box) Egress() *egress.Session { return b.egress }

// Gateway is the box's gateway: the tools it serves the agent.
func (b *Box) Gateway() *gateway.Gateway { return b.gateway }

// EgressSettings are the network rules from settings, in layers: the projects', then the user's and those always
// allowed.
func (b *Box) EgressSettings() [][]egress.Rule { return b.egressSettings() }

// GatewayLevels are the gateway's rules, by where they come from.
func (b *Box) GatewayLevels() gateway.Levels { return b.gatewayLevels() }

// Grants are the session's own gateway rules.
func (b *Box) Grants() []gateway.Rule { return b.grants() }

// Grant adds gateway rules to the session's own.
func (b *Box) Grant(rules []gateway.Rule) { b.grant(rules) }
