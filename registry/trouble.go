package registry

import "strings"

// Trouble is something gone wrong that the agent can be asked to fix or to explain: a
// failing test, a diagnostic, a failed command, a red CI check, a conflict. Every "ask to fix"
// is FixCommand with its Args, and its twin is ExplainCommand with the same Args.
type Trouble struct {
	Kind    string // "test-failure", "diagnostic", "command", "ci-failure", "conflict"
	Subject string // a line naming it: "TestRoundHalfEven/-3.5 failed"
	Context string // the failure, the diagnostic, the command and its output
	File    string // where it is, if it's in a file
	Line    int    // 1-based; 0 with no file
	EndLine int
	Fix     string // the fix request's text; "Fix this: <Subject>" if empty
}

// The commands every fix entry point and its twin run, with a Trouble's Args.
const (
	FixCommand     = "agent.fix"
	ExplainCommand = "explain.ask"
)

// Args are t as FixCommand and ExplainCommand take it.
func (t Trouble) Args() Args {
	a := Args{"kind": t.Kind, "subject": t.Subject, "context": t.Context}
	if t.File != "" {
		a["file"], a["line"], a["end_line"] = t.File, max(t.Line, 1), max(t.EndLine, t.Line, 1)
	}
	if t.Fix != "" {
		a["fix"] = t.Fix
	}
	return a
}

// TroubleOf is the Trouble in a, as Args made it.
func TroubleOf(a Args) Trouble {
	return Trouble{Kind: a.String("kind"), Subject: a.String("subject"), Context: a.String("context"), File: a.String("file"),
		Line: a.Int("line"), EndLine: a.Int("end_line"), Fix: a.String("fix")}
}

// FixText is what the fix request says.
func (t Trouble) FixText() string {
	if t.Fix != "" {
		return t.Fix
	}
	return "Fix this: " + strings.TrimSpace(t.Subject)
}

// ExplainLabel is the label of a menu item's explain twin.
const ExplainLabel = "Ask the agent to explain"

// IsFix is whether a menu item asks the agent to fix something, so it gets an explain twin.
func (m MenuItem) IsFix() bool { return m.Command == FixCommand }

// Twin is a fix item's explain twin: ExplainCommand with the same Args, beside it.
func (m MenuItem) Twin() MenuItem {
	return MenuItem{Name: m.Name + ".explain", Label: ExplainLabel, Group: m.Group, Order: m.Order, When: m.When,
		Command: ExplainCommand, Args: m.Args, Trouble: m.Trouble, Source: m.Source}
}

// WithTwins puts each fix item's explain twin right after it, unless one by that name is already there.
func WithTwins(items []MenuItem) []MenuItem {
	have := map[string]bool{}
	for _, m := range items {
		have[m.Name] = true
	}
	out := make([]MenuItem, 0, len(items))
	for _, m := range items {
		out = append(out, m)
		if tw := m.Twin(); m.IsFix() && !have[tw.Name] {
			out = append(out, tw)
		}
	}
	return out
}
