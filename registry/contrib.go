package registry

import (
	"encoding/json"
	"slices"
	"strings"
)

// Contribution is anything a feature registers for another to use, by kind and name: an agent tool, a prompt pack,
// a session kind, a HUD indicator, a status rule. A later one of a kind and name shadows the earlier until disposed.
type Contribution interface {
	ContributionKind() string
	ContributionName() string
}

func (r *reg) Contribute(c Contribution) Disposable {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := c.ContributionKind() + "\x00" + c.ContributionName()
	tok := r.contribs.push(k, c, r.tokens.Add(1))
	r.contribBy = nil
	r.contribGen++
	return disposeOnce(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.contribs.remove(k, tok)
		r.contribBy = nil
		r.contribGen++
	})
}

func (r *reg) Contribution(kind, name string) (Contribution, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.contribs.top(kind + "\x00" + name)
}

// Sourced is a contribution that says who registered it, for the faults it causes.
type Sourced interface{ ContributionSource() string }

func (r *reg) ContributionsGen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.contribGen
}

func (r *reg) ContributionKinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for k := range r.contribs {
		if kind, _, ok := strings.Cut(k, "\x00"); ok && !slices.Contains(out, kind) {
			out = append(out, kind)
		}
	}
	slices.Sort(out)
	return out
}

func (r *reg) Contributions(kind string) []Contribution {
	r.mu.Lock()
	defer r.mu.Unlock()
	if out, ok := r.contribBy[kind]; ok {
		return out[:len(out):len(out)]
	}
	var out []Contribution
	for k := range r.contribs {
		if strings.HasPrefix(k, kind+"\x00") {
			c, _ := r.contribs.top(k)
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b Contribution) int { return strings.Compare(a.ContributionName(), b.ContributionName()) })
	if r.contribBy == nil {
		r.contribBy = map[string][]Contribution{}
	}
	r.contribBy[kind] = out
	return out[:len(out):len(out)]
}

// AgentTool is a tool offered to agents beside Oriel's own: an LSP lookup, a plugin's query, the
// assistant's sessions. The agent sees it as mcp__oriel__<Name>. Run runs in the app; SessionOf(ctx) is the session
// that asked, AgentOf(ctx) the agent. Run may return an Acted to say what it did and how to take it back.
type AgentTool struct {
	Name, Desc string
	Params     map[string]string // name → type: "string", "number?"; or InputSchema
	// InputSchema is the input's JSON Schema, when Params can't say it: lists, enums.
	InputSchema json.RawMessage
	ReadOnly    bool // changes nothing: allowed without asking
	// For are the agents it's offered to: session kinds ("work") or utility agents ("assistant"). None is every
	// session's agent and no utility agent.
	For []string
	// Reads names the data it reads ("github", "linear"), which the assistant lists as its sources.
	Reads string
	// Confirm makes it an act that waits for the user's yes: what it will do, shown by the agent's owner before Run.
	// It returns Granted when this call needs no yes, and an error to refuse it.
	Confirm func(ctx Ctx, params map[string]any) (Confirmation, error)
	Run     func(ctx Ctx, params map[string]any) (any, error)
	// Adds makes it a part of another tool's answer rather than a tool of its own: when the tool named Adds is
	// called, the answer is each one's result under its Reads (or Name): agentbus's review requests beside GitHub's.
	Adds string
	// CoveredBy are MCP servers that already do what it does, by name or part of where they run (mcp.linear.app): an
	// agent that reaches one of its own, or through its sandbox's gateway, isn't offered it.
	CoveredBy []string
	Source    string
}

// PromptPack is a named prompt and the tools that go with it.
type PromptPack struct {
	Name, Prompt string
	Tools        []string
	Source       string
}

// StatusRule picks a session's status colour from its fields, or "" to leave it.
type StatusRule struct {
	Name   string
	Rule   func(session map[string]any) (string, error)
	Source string
}

func (AgentTool) ContributionKind() string    { return "agent.tool" }
func (t AgentTool) ContributionName() string  { return t.Name }
func (PromptPack) ContributionKind() string   { return "agent.pack" }
func (p PromptPack) ContributionName() string { return p.Name }
func (StatusRule) ContributionKind() string   { return "status.rule" }
func (s StatusRule) ContributionName() string { return s.Name }

// InspectSection is part of :Inspect: rows about what's under the cursor, shown in
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

// Font is a font file a plugin ships ([contributes] fonts), by its file name.
type Font struct {
	Name, Path, Source string
}

// Icon is an icon file a plugin ships ([contributes] icons), named by its file name without the
// extension.
type Icon struct {
	Name, Path, Source string
	Data               []byte // the file's contents in place of Path: a bundled plugin's
}

func (Font) ContributionKind() string   { return "font" }
func (f Font) ContributionName() string { return f.Name }
func (Icon) ContributionKind() string   { return "icon" }
func (i Icon) ContributionName() string { return i.Name }
