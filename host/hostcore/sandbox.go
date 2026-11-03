package hostcore

import (
	"context"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

// SandboxFactory makes the sandbox, gateway and egress proxy a sandboxed session runs in. Layer-3
// approvals (a held connection, a held gateway call) come back through approve, which shows the user a card
// and waits for the answer.
type SandboxFactory interface {
	Open(ctx context.Context, se core.SessionState, approve Approve, a SandboxAgent) (Sandbox, error)
	// Backends are the sandbox backends this machine can run.
	Backends() []string
	// Sweep removes what sandboxes no session uses left behind; attached reports whether root's agent is running,
	// known whether a session still has root. It returns what it removed.
	Sweep(ctx context.Context, attached, known func(root string) bool) []string
}

// SandboxAgent is the agent a sandbox opens for: its provider, which says what it needs there (agent.Sandboxed),
// and the session's directory for it (agent.StartSpec.StateDir).
type SandboxAgent struct {
	Provider agent.Provider
	StateDir string
}

// Approve puts an ask to the session's rules, the app's hooks, then the user, and waits for the answer.
type Approve func(ctx context.Context, ask core.Ask) (Approval, error)

// Approval is an answer to an ask, with who gave it and what they said, for a refusal the agent reads.
type Approval struct {
	Allow bool
	Scope string // a wider rule the user chose ("*.golang.org"), or ""
	By    string // "the user", "rule net(…)", the hook's source
	Note  string
}

// Sandbox is one session's sandbox.
type Sandbox interface {
	Exec() agent.Exec
	Spec() *agent.Sandbox
	Close() error
}

// Keyed runs fn, a git operation of session id's that may wait on a security-key touch, asking for the touch while
// it waits; what and project say what it is on the card.
type Keyed func(ctx context.Context, id core.SessionID, what, project string, fn func(ctx context.Context) error) error

// ToolsReq asks the app for the tools it offers an agent: a session's (Agent is its kind), or a utility agent's (no
// Session; Agent is its name).
type ToolsReq struct {
	Session core.SessionID `json:"session,omitempty"`
	Agent   string         `json:"agent,omitempty"`
}
