package registry

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/liamg/oriel/text"
)

// ExplainRequest is something to explain: what kind of thing it is,
// a line naming it, and everything an explainer needs to know about it.
type ExplainRequest struct {
	Kind    string // "test-failure", "diagnostic", "selection", "unit", "change"
	Subject string // "TestRoundHalfEven/-3.5 failed"
	Context string // the failure, the test's code, the code under test…
	Files   []string
	Depth   string // ExplainBrief (""), ExplainDetailed or ExplainWhy
	// Format, when set, is the shape the answer must take instead of markdown prose, for a caller that parses it
	// (the walkthrough's JSON steps); it replaces the explainer's own instructions on length and form.
	Format string
}

// How deep an explanation goes: brief by default, detailed on demand, and why · history, whose Context
// carries the blame, the commits, the PR and the threads that touched it.
const (
	ExplainBrief    = "brief"
	ExplainDetailed = "detailed"
	ExplainWhy      = "why"
)

// Explainer explains things without changing anything: it only answers, in markdown. The agent's (a tool-less fork
// of the session's, AI) registers one; until something does, "ask to explain" is unavailable.
type Explainer struct {
	Name    string
	Explain func(ctx context.Context, r ExplainRequest) (markdown string, err error)
	Order   int           // the first by Order answers
	Label   func() string // who answers, under each answer: "fork of claude · knows this session"
	// Byline says who wrote a long answer, under it: "written by claude from the session's own context". Without
	// one, it's "written by " and the Label.
	Byline func() string
	Source string
}

// Explain asks the first explainer about r, and says which answered.
func Explain(ctx context.Context, reg Registry, r ExplainRequest) (string, Explainer, error) {
	es := Explainers(reg)
	if len(es) == 0 {
		return "", Explainer{}, errors.New("nothing can explain yet: it needs an agent")
	}
	var md string
	err := Call(func() (err error) { md, err = es[0].Explain(ctx, r); return err })
	return md, es[0], err
}

// WrittenBy is the explainer's byline, or "written by " and its label (or name).
func (e Explainer) WrittenBy() string {
	var s string
	Call(func() error {
		switch {
		case e.Byline != nil:
			s = e.Byline()
		case e.Label != nil:
			s = "written by " + e.Label()
		}
		return nil
	})
	if s == "" {
		s = "written by " + e.Name
	}
	return s
}

func (Explainer) ContributionKind() string   { return "explainer" }
func (e Explainer) ContributionName() string { return e.Name }

// Explainers are the explainers by Order, then name.
func Explainers(r Contributions) []Explainer {
	var out []Explainer
	out = append(out, ContributionsOf[Explainer](r)...)
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

// EditorWindow is an editor window: its id, file, the presenter showing it ("" while it's text), and its cursor.
type EditorWindow struct {
	ID        string
	Path      string
	Presenter string
	Cursor    text.Pos
	Top       int          // the first line it shows, 0-based
	Text      *text.Buffer // the buffer it shows
	Caret     uint64       // the ui.ID its cursor was drawn as last frame, for popups at the cursor; 0 before
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
func EditorActions(r Contributions, path string) []EditorAction {
	var out []EditorAction
	for _, a := range ContributionsOf[EditorAction](r) {
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
