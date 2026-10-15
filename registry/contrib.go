package registry

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/ui"
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
	tok := r.contribs.push(k, c)
	r.contribGen++
	return disposeOnce(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.contribs.remove(k, tok)
		r.contribGen++
	})
}

func (r *reg) ContributionsGen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.contribGen
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

// AgentTool is a tool offered to every session's agent beside Oriel's own (agents.md): an LSP lookup, a plugin's
// query. The agent sees it as mcp__oriel__<Name>. Run runs in the app; SessionOf(ctx) is the session that asked.
type AgentTool struct {
	Name, Desc string
	Params     map[string]string // name → type: "string", "number?"
	ReadOnly   bool              // changes nothing: allowed without asking
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

// Indicator is a small live readout in a slot (ui.md, "Indicators"): the title bar's right, the status line's sides, the
// HUD's panels, a panel's own slot. What Build returns is kept and drawn until the indicator is built again: every
// Every, when one of the events named in On happens, or when it's refreshed (the hud.refresh command). A node that
// reads state as it's drawn (a ui.Func) is live without any of them.
type Indicator struct {
	Name  string // unique among indicators: "deploys"
	Slot  string // SlotTitleBar, SlotStatusLeft, SlotStatusRight, SlotHUD, or a slot a panel draws: SlotHUDToday
	Order int    // in its slot, among the slot's views: titlebar.right.40-usage is 40
	// Title is a HUD panel's heading, Note what it says at the heading's right ("3 online"), and Span how many of the
	// HUD's four columns it takes (1 if 0). Help is its help page and anchor.
	Title, Help string
	Note        func() string
	Span        int
	Every       time.Duration
	On          []string // core event names that build it again: "WorkerChanged"
	Build       func(ctx Ctx) (ui.Node, error)
	Source      string
}

// The slots indicators go in.
const (
	SlotTitleBar    = "titlebar.right"
	SlotStatusLeft  = "statusline.left"
	SlotStatusRight = "statusline.right"
	SlotHUD         = "hud"       // the HUD's panels
	SlotHUDToday    = "hud.today" // under the HUD's Today panel: the next meeting
)

// Indicators are the indicators in slot, by Order then name.
func Indicators(r Registry, slot string) []Indicator {
	var out []Indicator
	for _, c := range r.Contributions("hud.indicator") {
		if i := c.(Indicator); i.Slot == slot {
			out = append(out, i)
		}
	}
	slices.SortStableFunc(out, func(a, b Indicator) int { return cmp.Compare(a.Order, b.Order) })
	return out
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
func (Indicator) ContributionKind() string     { return "hud.indicator" }
func (t Indicator) ContributionName() string   { return t.Name }
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
