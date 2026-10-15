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
