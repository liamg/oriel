package hostapi

import (
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/egress"
	"github.com/liamg/oriel/sandbox/policy"
)

// Permissions is what a sandboxed session's agent may do: each gateway tool and each network rule in force, with
// where its answer comes from, the connections it made or tried, and the destinations waiting on a card.
type Permissions struct {
	// Sandboxed is false for an agent on the host, which none of this applies to.
	Sandboxed bool
	// Filtered is false when the egress proxy is off: the sandbox has plain internet.
	Filtered bool
	Tools    []ToolPermission
	Hosts    []egress.Rule
	Conns    []egress.Event
	Waiting  []string
}

// ToolPermission is one gateway tool: what a call meets, its source, and the session's own rule for it.
type ToolPermission struct {
	policy.ToolPolicy
	Source  string
	Session string
}

// Workspace is where a session's agent works: its directories, and the sandbox it runs in.
type Workspace struct {
	Root      string
	Sandboxed bool
	// Running says the agent's sandbox is up now.
	Running bool
	Backend string
	Paths   []WorkspacePath
	// Rebuild says the backend can build the session's image again.
	Rebuild bool
	sandbox.Description
}

// WorkspacePath is a directory a session uses: on the host, and where its sandboxed agent finds it.
type WorkspacePath struct {
	Name  string
	Host  string
	Agent string // "" where the agent sees it at the same path
	Note  string
}

// Where a rule comes from, as the Permissions view names it.
const (
	SourceSession  = "this session"
	SourceProject  = "project settings"
	SourceSettings = "your settings"
	SourceAlways   = "always, on this machine"
	SourceReadOnly = "read-only, allowed by default"
	SourceDefault  = "built-in default"
)
