//oriel:kind work

package sessions

import (
	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// registryKinds is the session host's KindSource: the kinds registered with the app, a plugin's as well as the
// built-ins.
type registryKinds struct{ r registry.Registry }

func (k registryKinds) Kind(kind core.Kind) (core.KindSpec, bool) {
	sk, ok := registry.KindOf(k.r, kind)
	if !ok {
		return core.KindSpec{}, false
	}
	return sk.Spec(), true
}

// registryProviders are the agent providers registered with the app, by Order.
func registryProviders(r registry.Contributions) []agent.Provider {
	var out []agent.Provider
	for _, p := range registry.AgentProviders(r) {
		out = append(out, p.Provider)
	}
	return out
}

// contributeKinds registers the work and direct kinds and the Sessions and host services, and gives the host the
// registry's kinds and agent providers.
func (x *Sessions) contributeKinds(h plugin.Host) {
	x.Add(h.Contribute(registry.KindEntry{SessionKind: registry.Kind{KindSpec: host.WorkKind}, Source: "builtin:sessions"}))
	x.Add(h.Contribute(registry.KindEntry{SessionKind: registry.Kind{KindSpec: host.DirectKind}, Source: "builtin:sessions"}))
	// A work session's plan, before it ships.
	x.Add(h.Contribute(registry.ShipGate{Name: "plan", Title: "Plan approved", Order: 10, Check: func(id string) registry.Gate {
		se, ok := sessionSnapshot(h, core.SessionID(id))
		switch {
		case !ok:
			return registry.Gate{}
		case se.Planning():
			return registry.Gate{State: registry.GatePending, Note: "planning"}
		case se.StartWork && se.Started:
			return registry.Gate{State: registry.GatePass}
		case se.Ticket != "":
			return registry.Gate{State: registry.GatePass, Note: se.Ticket}
		case unplanned(se):
			return registry.Gate{State: registry.GateSkipped, Note: "unplanned"}
		}
		return registry.Gate{}
	}}))
	x.Add(h.Contribute(registry.Service{Name: registry.SessionsService, Impl: sessionsService{x}, Source: "builtin:sessions"}))
	if w := x.Host; w != nil {
		for _, part := range []struct {
			name string
			impl any
		}{{registry.HostSessionsService, host.SessionCalls(w)},
			{registry.HostSandboxesService, host.SandboxCalls(w)}, {registry.HostShipsService, host.ShipCalls(w)},
			{registry.HostCodeHostsService, host.CodeHostCalls(w)}, {registry.HostReposService, host.RepoCalls(w)},
			{registry.HostJobsService, host.JobCalls(w)}, {registry.HostStatsService, host.StatsCalls(w)}} {
			x.Add(h.Contribute(registry.Service{Name: part.name, Impl: part.impl, Source: "builtin:sessions"}))
		}
		w.SetKinds(registryKinds{h})
		x.Add(kit.OnDispose(func() { w.SetKinds(nil) }))
		w.SetProviders(func() []agent.Provider { return registryProviders(h) })
		x.Add(kit.OnDispose(func() { w.SetProviders(nil) }))
	}
}

// sessionsService is the registry's Sessions service.
type sessionsService struct{ x *Sessions }

func (s sessionsService) Front() (core.SessionState, bool) {
	return sessionSnapshot(s.x.h, s.x.h.State().Focus)
}

func (s sessionsService) Get(id core.SessionID) (core.SessionState, bool) {
	return sessionSnapshot(s.x.h, id)
}

func (s sessionsService) FromArgs(a registry.Args) (core.SessionState, error) {
	return registry.SessionFromArgs(a, s.x.h.State().Focus, s.Get)
}

func (s sessionsService) Start(ctx registry.Ctx, a registry.Args) (core.SessionID, error) {
	return s.x.start(ctx, a)
}

// unplanned says se is work started with no plan or ticket.
func unplanned(se core.SessionState) bool {
	d, _ := core.DataOf[core.WorkData](se)
	return d.Unplanned
}

func sessionSnapshot(h plugin.Host, id core.SessionID) (core.SessionState, bool) {
	if id == "" {
		return core.SessionState{}, false
	}
	se, ok := h.Session(id)
	if !ok {
		return core.SessionState{}, false
	}
	return se.Snapshot(), true
}
