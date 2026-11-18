package registry

import (
	"cmp"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
)

// ShipGate is one of the "before this ships" gates on the Ship page and the Raise PR tab: the plan
// approved, threads resolved, tests run, the verify quiz, an AI review. Check runs on the app's
// loop as the page is drawn, for a session, so it answers from what it has. Gates say where a change stands; what
// stops a commit or a raise is a veto hook on ShipCommit or ShipRaise.
type ShipGate struct {
	Name   string // unique among gates: "threads"
	Title  string // "All threads resolved"
	Order  int
	Check  func(session string) Gate
	Source string
}

// Gate is a gate's state for a session.
type Gate struct {
	State GateState // "" leaves the gate out
	Note  string    // dim text at its right: "9", "38 / 59", "go test 2m ago", "optional"
	// Action is a button on the gate's row that works towards it ("Verify…"), running Command with Args.
	Action  string
	Command string
	Args    Args
}

// GateState is where a gate stands.
type GateState string

// Gate states: met; not met but not in the way (a "!"); failed; still to come; one that's up to you; and one that
// doesn't apply to this session.
const (
	GatePass     GateState = "pass"
	GateWarn     GateState = "warn"
	GateFail     GateState = "fail"
	GatePending  GateState = "pending"
	GateOptional GateState = "optional"
	GateSkipped  GateState = "skipped"
)

func (ShipGate) ContributionKind() string   { return "ship.gate" }
func (g ShipGate) ContributionName() string { return g.Name }

// ShipGateState is a gate with its state for a session.
type ShipGateState struct {
	ShipGate
	Gate
}

// ShipGates are the gates for a session of kind, by Order, each with its state; one that panics or answers no state
// is left out. A session kind that names its Gates gets only those.
func ShipGates(r Contributions, session, kind string) []ShipGateState {
	var only []string
	if k, ok := KindOf(r, core.Kind(kind)); ok {
		IgnorePanic(func() { only = k.Gates() })
	}
	var gs []ShipGate
	for _, g := range ContributionsOf[ShipGate](r) {
		if g.Check != nil && (only == nil || slices.Contains(only, g.Name)) {
			gs = append(gs, g)
		}
	}
	slices.SortFunc(gs, func(a, b ShipGate) int { return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name)) })
	var out []ShipGateState
	for _, g := range gs {
		var st Gate
		IgnorePanic(func() { st = g.Check(session) })
		if st.State != "" {
			out = append(out, ShipGateState{ShipGate: g, Gate: st})
		}
	}
	return out
}

// PullRequest is a session's raised PR as its actions see it.
type PullRequest struct {
	Session, Project string
	Repo             string // owner/name
	Number           int
	Title, URL       string
	State            core.PRState // core.PROpen or core.PRClosed
	Draft, Merged    bool
}

// Args are the PR as a command's arguments.
func (p PullRequest) Args() Args {
	return Args{"session": p.Session, "project": p.Project, "repo": p.Repo, "number": p.Number, "title": p.Title, "url": p.URL,
		"state": string(p.State), "draft": p.Draft, "merged": p.Merged}
}

// PRAction is a button on a raised PR, beside Ready for review and Open on GitHub: the Ship page's
// PR step and the Pull request panel. Label names it for the PR, "" leaving it out; it runs on the app's loop as
// the page is drawn. A click runs Command with Args and the PR's (PullRequest.Args).
type PRAction struct {
	Name    string
	Order   int
	Label   func(pr PullRequest) string
	Help    string
	Command string
	Args    Args
	Source  string
}

func (PRAction) ContributionKind() string   { return "ship.pr.action" }
func (a PRAction) ContributionName() string { return a.Name }

// PRActionState is an action labelled for a PR.
type PRActionState struct {
	PRAction
	PR    PullRequest
	Label string
}

// RunArgs are what its command runs with: its Args, then the PR's.
func (a PRActionState) RunArgs() Args {
	out := Args{}
	for k, v := range a.Args {
		out[k] = v
	}
	for k, v := range a.PR.Args() {
		out[k] = v
	}
	return out
}

// PRActions are the actions for pr, by Order; one that panics or gives no label is left out.
func PRActions(r Contributions, pr PullRequest) []PRActionState {
	var as []PRAction
	for _, a := range ContributionsOf[PRAction](r) {
		if a.Label != nil {
			as = append(as, a)
		}
	}
	slices.SortFunc(as, func(a, b PRAction) int { return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name)) })
	var out []PRActionState
	for _, a := range as {
		label := ""
		IgnorePanic(func() { label = a.Label(pr) })
		if label != "" {
			out = append(out, PRActionState{PRAction: a, PR: pr, Label: label})
		}
	}
	return out
}
