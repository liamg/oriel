package uireg_test

import (
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
)

// kindsReg has a work kind that ships and a plan kind, and front as the session in front.
func kindsReg(front *core.SessionState) uireg.Registry {
	r := uireg.New()
	r.Contribute(registry.KindEntry{SessionKind: registry.Kind{KindSpec: registry.KindSpec{Kind: "work", Order: 10,
		Caps: []core.Capability{core.Ships, core.AddProjects}}}})
	r.Contribute(registry.KindEntry{SessionKind: registry.Kind{KindSpec: registry.KindSpec{Kind: "plan", Order: 20}}})
	r.Contribute(registry.Service{Name: registry.SessionsService, Impl: testSessions{front}})
	return r
}

// testSessions are the Sessions service over one session in front, or none.
type testSessions struct{ front *core.SessionState }

func (s testSessions) Front() (core.SessionState, bool) {
	if s.front == nil {
		return core.SessionState{}, false
	}
	return *s.front, true
}

func (s testSessions) Get(id core.SessionID) (core.SessionState, bool) {
	if s.front == nil || s.front.ID != id {
		return core.SessionState{}, false
	}
	return *s.front, true
}

func (s testSessions) FromArgs(a registry.Args) (core.SessionState, error) {
	return registry.SessionFromArgs(a, "", s.Get)
}

func (testSessions) Start(registry.Ctx, registry.Args) (core.SessionID, error) { return "", nil }
