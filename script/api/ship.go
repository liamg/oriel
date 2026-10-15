package api

import (
	"fmt"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

//oriel:api name=ship cap=ui
type Ship struct{ a *APIs }

//oriel:api doc="Add a \"before this ships\" gate to the Ship page and the Raise PR tab: { name, title, order (50 by default), check = fn(session) }. check returns a state (pass, warn, fail, pending or optional; nil leaves the gate out) or { state, note }, the note dim text at its right. It runs on the app's loop as the page is drawn, so keep it quick. A gate informs; a veto hook on ShipCommit or ShipRaise is what stops one."
func (s Ship) Gate(spec script.Table, where string) error {
	fn, _ := spec["check"].(*script.Fn)
	g := registry.ShipGate{Name: str(spec, "name"), Title: str(spec, "title"), Order: 50, Source: where}
	if g.Name == "" || fn == nil {
		return fmt.Errorf("a ship gate needs a name and a check function")
	}
	if g.Title == "" {
		g.Title = g.Name
	}
	if o, ok := spec["order"].(float64); ok {
		g.Order = int(o)
	}
	g.Check = func(session string) registry.Gate {
		out, err := s.a.VM.Call(fn, session)
		if err != nil || len(out) == 0 {
			return registry.Gate{}
		}
		switch v := out[0].(type) {
		case string:
			return registry.Gate{State: v}
		case script.Table:
			return registry.Gate{State: str(v, "state"), Note: str(v, "note")}
		}
		return registry.Gate{}
	}
	s.a.own(s.a.Host.Contribute(g))
	return nil
}

//oriel:api doc="Add a button to a raised pull request, beside Ready for review and Open on GitHub (the Ship page and the Pull request panel): { name, label = 'text' or fn(pr), command, args, run = fn(pr), order (50 by default), help }. pr is { session, project, repo (owner/name), number, title, url, state (open or closed), draft, merged }; a label function returning nil leaves the button off that PR, and runs as the page is drawn, so keep it quick. A click runs command with args and pr's fields, or run(pr)."
func (s Ship) Action(spec script.Table, where string) error {
	a := prActionSpec(s.a, spec, where)
	if a.Name == "" || a.Command == "" || a.Label == nil {
		return fmt.Errorf("a PR action needs a name, a label and a command or run function")
	}
	s.a.own(s.a.Host.Contribute(a))
	return nil
}

// prActionSpec reads oriel.ship.action's spec.
func prActionSpec(a *APIs, spec script.Table, where string) registry.PRAction {
	pa := registry.PRAction{Name: str(spec, "name"), Order: 50, Help: str(spec, "help"), Command: str(spec, "command"),
		Args: registry.Args(tableOf(spec["args"])), Source: where}
	if o, ok := spec["order"].(float64); ok {
		pa.Order = int(o)
	}
	if fn, ok := spec["run"].(*script.Fn); ok {
		pa.Command = a.anon(fn)
	}
	switch l := spec["label"].(type) {
	case string:
		pa.Label = func(registry.PullRequest) string { return l }
	case *script.Fn:
		pa.Label = func(pr registry.PullRequest) string {
			out, err := a.VM.Call(l, script.Table(pr.Args()))
			if err != nil || len(out) == 0 {
				return ""
			}
			s, _ := out[0].(string)
			return s
		}
	}
	return pa
}
