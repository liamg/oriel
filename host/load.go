package host

import (
	"context"
	"os"
	"runtime"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/sysload"
)

// machineState is this machine as the app shows it, with a fresh reading of its load.
func (w *Host) machineState() core.MachineState { return w.machineStateWith(w.opts.Load()) }

// machineStateWith is this machine as the app shows it, with the reading r.
func (w *Host) machineStateWith(r sysload.Reading) core.MachineState {
	host, _ := os.Hostname()
	var providers []string
	if w.opts.Providers != nil {
		for _, p := range w.opts.Providers.List() {
			providers = append(providers, p.ID())
		}
	}
	var sandboxes []string
	if w.opts.Sandbox != nil {
		sandboxes = w.opts.Sandbox.Backends()
	}
	return core.MachineState{Host: host, OS: runtime.GOOS, Arch: runtime.GOARCH, Version: w.opts.Version,
		Providers: providers, Sandboxes: sandboxes, Cores: r.Cores, CPU: r.CPU / 100, Mem: r.MemUsed, MemTotal: r.MemTotal,
		Sampled: w.opts.Now().UTC(), SignedOut: w.runPart.LostLogins()}
}

// SampleLoad reports the machine's load each time s says it moved, until ctx ends.
func (w *Host) SampleLoad(ctx context.Context, s *sysload.Sampler) {
	defer w.env.Recover("SampleLoad", nil)
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.closed:
			return
		case r := <-s.Changes():
			func() {
				defer w.env.Recover("SampleLoad", nil)
				_ = w.emit(core.MachineChanged{Machine: w.machineStateWith(r)})
			}()
		}
	}
}
