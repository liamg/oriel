package sessionkit

import (
	"errors"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

var ErrNoSessions = errors.New("there are no sessions in this window")

// The session host's parts' calls, each its own service, or why there are none.
func SessionCalls(r registry.Registry) (hostapi.SessionCalls, error) {
	return hostPart[hostapi.SessionCalls](r, hostapi.HostSessionsService)
}
func SandboxCalls(r registry.Registry) (hostapi.SandboxCalls, error) {
	return hostPart[hostapi.SandboxCalls](r, hostapi.HostSandboxesService)
}
func ShipCalls(r registry.Registry) (hostapi.ShipCalls, error) {
	return hostPart[hostapi.ShipCalls](r, hostapi.HostShipsService)
}
func CodeHostCalls(r registry.Registry) (hostapi.CodeHostCalls, error) {
	return hostPart[hostapi.CodeHostCalls](r, hostapi.HostCodeHostsService)
}
func RepoCalls(r registry.Registry) (hostapi.RepoCalls, error) {
	return hostPart[hostapi.RepoCalls](r, hostapi.HostReposService)
}
func JobCalls(r registry.Registry) (hostapi.JobCalls, error) {
	return hostPart[hostapi.JobCalls](r, hostapi.HostJobsService)
}
func HostWiring(r registry.Registry) (hostapi.Wiring, error) {
	return hostPart[hostapi.Wiring](r, hostapi.HostWiringService)
}
func StatsCalls(r registry.Registry) (hostapi.StatsCalls, error) {
	return hostPart[hostapi.StatsCalls](r, hostapi.HostStatsService)
}

func hostPart[T any](r registry.Registry, name string) (T, error) {
	if w, ok := registry.ServiceOf[T](r, name); ok {
		return w, nil
	}
	var none T
	return none, ErrNoSessions
}

// HasSessions says the window has a session host.
func HasSessions(r registry.Registry) bool {
	_, err := SessionCalls(r)
	return err == nil
}

// SessionFrom is the session a command's {id} names, else the one in front (Sessions.FromArgs).
func SessionFrom(h plugin.Host, a registry.Args) (core.SessionState, error) {
	s, ok := registry.SessionsOf(h)
	if !ok {
		return core.SessionState{}, ErrNoSessions
	}
	return s.FromArgs(a)
}

// SessionArgs is SessionFrom, and the session host's session calls.
func SessionArgs(h plugin.Host, a registry.Args) (core.SessionState, hostapi.SessionCalls, error) {
	se, err := SessionFrom(h, a)
	if err != nil {
		return se, nil, err
	}
	w, err := SessionCalls(h)
	return se, w, err
}
