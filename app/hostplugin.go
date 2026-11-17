package app

import (
	"context"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// hostPlugin publishes the session host's parts as the registry's host.* services, which every feature reaches the
// host through; with no host, none.
type hostPlugin struct {
	kit.Disposer
	Host *host.Host
}

func (x *hostPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "host"} }

func (x *hostPlugin) Activate(_ context.Context, h plugin.Host) error {
	w := x.Host
	if w == nil {
		return nil
	}
	for name, impl := range map[string]any{
		hostapi.HostSessionsService: hostapi.SessionCalls(w), hostapi.HostSandboxesService: hostapi.SandboxCalls(w),
		hostapi.HostShipsService: hostapi.ShipCalls(w), hostapi.HostCodeHostsService: hostapi.CodeHostCalls(w),
		hostapi.HostReposService: hostapi.RepoCalls(w), hostapi.HostJobsService: hostapi.JobCalls(w), hostapi.HostStatsService: hostapi.StatsCalls(w),
		hostapi.HostWiringService: hostapi.Wiring(w),
	} {
		x.Add(h.Contribute(registry.Service{Name: name, Impl: impl}))
	}
	return nil
}
