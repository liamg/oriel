package registry

import (
	"cmp"
	"context"
	"slices"
	"strings"
)

// ExplainRequest is something to explain (ide.md, "Explain sits next to fix everywhere"): what kind of thing it is,
// a line naming it, and everything an explainer needs to know about it.
type ExplainRequest struct {
	Kind    string // "test-failure", "diagnostic", "selection"
	Subject string // "TestRoundHalfEven/-3.5 failed"
	Context string // the failure, the test's code, the code under test…
	Files   []string
}

// Explainer explains things without changing anything: it only answers, in markdown. The agent's (a tool-less fork
// of the session's, AI) registers one; until something does, "ask to explain" is unavailable.
type Explainer struct {
	Name    string
	Explain func(ctx context.Context, r ExplainRequest) (markdown string, err error)
	Order   int // the first by Order answers
	Source  string
}

func (Explainer) ContributionKind() string   { return "explainer" }
func (e Explainer) ContributionName() string { return e.Name }

// Explainers are the explainers by Order, then name.
func Explainers(r Registry) []Explainer {
	var out []Explainer
	for _, c := range r.Contributions("explainer") {
		out = append(out, c.(Explainer))
	}
	slices.SortFunc(out, func(a, b Explainer) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}

// EditorAction is a button at the right of an editor's breadcrumb, for files it applies to: run this file's tests,
// watch, coverage, a markdown file's Preview. Its command runs with the window it was clicked in added to Args as
// "window", unless Args names one.
type EditorAction struct {
	Name    string
	Label   string
	LabelOn string // the label while it's on, if it differs: Preview's "Edit"
	Icon    string // before the label: a built-in icon's name, or a glyph
	IconOn  string // the icon while it's on, if it differs
	Command string
	Args    Args
	When    func(path string) bool      // nil: every file
	On      func(w EditorWindow) bool   // lit while it says so, for the window it's in: a toggle
	Note    func(w EditorWindow) string // dim text after the label: a count
	Order   int
	Help    string
	Source  string
}

// EditorWindow is the editor window an action is drawn in: its id, file, and the presenter showing it ("" while
// it's text).
type EditorWindow struct {
	ID        string
	Path      string
	Presenter string
}

// Shown is whether the action is on in w, and the icon and label it shows then.
func (a EditorAction) Shown(w EditorWindow) (on bool, icon, label string) {
	if a.On != nil {
		Call(func() error { on = a.On(w); return nil })
	}
	icon, label = a.Icon, a.Label
	if on && a.IconOn != "" {
		icon = a.IconOn
	}
	if on && a.LabelOn != "" {
		label = a.LabelOn
	}
	return on, icon, label
}

func (EditorAction) ContributionKind() string   { return "editor.action" }
func (a EditorAction) ContributionName() string { return a.Name }

// EditorActions are the actions for a file, by Order.
func EditorActions(r Registry, path string) []EditorAction {
	var out []EditorAction
	for _, c := range r.Contributions("editor.action") {
		a := c.(EditorAction)
		ok := a.When == nil
		if !ok {
			Call(func() error { ok = a.When(path); return nil })
		}
		if ok {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b EditorAction) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}
