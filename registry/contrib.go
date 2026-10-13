package registry

import (
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/ui"
)

// Contribution is anything a feature registers for another to use, by kind and name: an agent tool, a prompt pack,
// a session kind, a HUD tat, a status rule. A later one of a kind and name shadows the earlier until disposed.
type Contribution interface {
	ContributionKind() string
	ContributionName() string
}

func (r *reg) Contribute(c Contribution) Disposable {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := c.ContributionKind() + "\x00" + c.ContributionName()
	tok := r.contribs.push(k, c)
	return disposeOnce(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.contribs.remove(k, tok)
	})
}

func (r *reg) Contributions(kind string) []Contribution {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Contribution
	for k := range r.contribs {
		if strings.HasPrefix(k, kind+"\x00") {
			c, _ := r.contribs.top(k)
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b Contribution) int { return strings.Compare(a.ContributionName(), b.ContributionName()) })
	return out
}

// AgentTool is a tool offered to a session's agent over MCP (agents.md); AI-1 and AI-7 offer them.
type AgentTool struct {
	Name, Desc string
	Params     map[string]string // name → type: "string", "number?"
	Run        func(ctx Ctx, params map[string]any) (any, error)
	Source     string
}

// PromptPack is a named prompt and the tools that go with it (AI-4).
type PromptPack struct {
	Name, Prompt string
	Tools        []string
	Source       string
}

// SessionKind is a kind of session: its pack, the gates before it ships, and its status rule (SE-1).
type SessionKind struct {
	Name, Pack, Status string
	Gates              []string
	Source             string
}

// Tat is a HUD tat: a small view in a slot, built again every Every (SH-8).
type Tat struct {
	Name, Slot string
	Every      time.Duration
	Build      func(ctx Ctx) (ui.Node, error)
	Source     string
}

// StatusRule picks a session's status colour from its fields, or "" to leave it (SH-2's colours).
type StatusRule struct {
	Name   string
	Rule   func(session map[string]any) (string, error)
	Source string
}

func (AgentTool) ContributionKind() string     { return "agent.tool" }
func (t AgentTool) ContributionName() string   { return t.Name }
func (PromptPack) ContributionKind() string    { return "agent.pack" }
func (p PromptPack) ContributionName() string  { return p.Name }
func (SessionKind) ContributionKind() string   { return "session.kind" }
func (k SessionKind) ContributionName() string { return k.Name }
func (Tat) ContributionKind() string           { return "hud.tat" }
func (t Tat) ContributionName() string         { return t.Name }
func (StatusRule) ContributionKind() string    { return "status.rule" }
func (s StatusRule) ContributionName() string  { return s.Name }

// InspectSection is part of :Inspect (scripting.md, "Introspection"): rows about what's under the cursor, shown in
// Order. The editor gives its syntax and decorations; anything else can add its own.
type InspectSection struct {
	Name   string // "TREE-SITTER"
	Order  int
	Rows   func() []InspectRow
	Source string
}

// InspectRow is one line of a section: what, about it, and where it comes from.
type InspectRow struct {
	Key, Value, Source string
	Faded              bool // shadowed, or not here yet
}

func (InspectSection) ContributionKind() string   { return "inspect.section" }
func (s InspectSection) ContributionName() string { return s.Name }
