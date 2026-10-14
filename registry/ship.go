package registry

import (
	"cmp"
	"slices"
	"strings"
)

// ShipGate is one of the "before this ships" gates on the Ship page and the Raise PR tab (ui.md 18): the plan
// approved, threads resolved, the review ledger, tests run, the verify quiz, an AI review. Check runs on the app's
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
	State string // GatePass, GateWarn, GateFail, GatePending or GateOptional; "" leaves the gate out
	Note  string // dim text at its right: "9", "38 / 59", "go test 2m ago", "before raise"
}

// Gate states: met; not met but not in the way (a "!"); failed; still to come; and one that's up to you.
const (
	GatePass     = "pass"
	GateWarn     = "warn"
	GateFail     = "fail"
	GatePending  = "pending"
	GateOptional = "optional"
)

func (ShipGate) ContributionKind() string   { return "ship.gate" }
func (g ShipGate) ContributionName() string { return g.Name }

// GateResult is a gate with its state for a session.
type GateResult struct {
	ShipGate
	Gate
}

// ShipGates are the gates for a session of kind, by Order, each with its state; one that panics or answers no state
// is left out. A session kind that names its Gates (SessionKind) gets only those.
func ShipGates(r Registry, session, kind string) []GateResult {
	var only []string
	for _, c := range r.Contributions("session.kind") {
		if k := c.(SessionKind); k.Name == kind && len(k.Gates) > 0 {
			only = k.Gates
		}
	}
	var gs []ShipGate
	for _, c := range r.Contributions("ship.gate") {
		g := c.(ShipGate)
		if g.Check != nil && (only == nil || slices.Contains(only, g.Name)) {
			gs = append(gs, g)
		}
	}
	slices.SortFunc(gs, func(a, b ShipGate) int { return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name)) })
	var out []GateResult
	for _, g := range gs {
		var st Gate
		Call(func() error { st = g.Check(session); return nil })
		if st.State != "" {
			out = append(out, GateResult{ShipGate: g, Gate: st})
		}
	}
	return out
}
