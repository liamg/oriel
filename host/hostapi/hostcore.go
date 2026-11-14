package hostapi

import (
	"context"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

// OffBranch is a project whose worktree is on a branch other than the session's for it, which Oriel won't push.
type OffBranch struct {
	Session string `json:"session"`           // the session's branch for the project
	Current string `json:"current,omitempty"` // the branch checked out; "" when detached
	Refused string `json:"refused,omitempty"` // why Current can't be made the session's branch; "" when it can
}

// Draft is a commit message or PR drafted for a project: by the agent (its provider's name) or "you". Stale says
// the code has moved on since it was written.
type Draft struct {
	Title string    `json:"title,omitempty"`
	Body  string    `json:"body,omitempty"`
	By    string    `json:"by,omitempty"`
	Saved time.Time `json:"saved,omitzero"`
	Stale bool      `json:"stale,omitempty"`
	Code  string    `json:"-"` // the code's fingerprint when it was written (DraftCode)
}

// Empty says nothing was drafted.
func (d Draft) Empty() bool { return strings.TrimSpace(d.Title+d.Body) == "" }

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
	All   bool   // an always for all sessions, which the host keeps; the session's own otherwise
	By    string // "the user", "rule net(…)", the hook's source
	Note  string
}

// Sandbox is one session's sandbox.
type Sandbox interface {
	Exec() agent.Exec
	Spec() *agent.Sandbox
	Close() error
}

// ToolsReq asks the app for the tools it offers an agent: a session's (Agent is its kind), or a utility agent's (no
// Session; Agent is its name).
type ToolsReq struct {
	Session core.SessionID `json:"session,omitempty"`
	Agent   string         `json:"agent,omitempty"`
}

// DraftByYou marks a draft the user wrote: the agent never overwrites it.
const DraftByYou = "you"

// Sanitise reduces a string to a name safe for a directory and a git branch.
func Sanitise(s string) string {
	var sb strings.Builder
	for _, r := range strings.TrimSpace(strings.ToLower(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		case r == ' ', r == '.', r == '/':
			sb.WriteRune('-')
		}
	}
	out := sb.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return strings.Trim(out, "-")
}
