package host

import (
	"context"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/sysload"
)

// ProviderInfo is a provider as the launcher shows it.
type ProviderInfo struct {
	ID      string             `json:"id"`
	Install agent.Install      `json:"install"`
	Has     agent.Features     `json:"has"` // what its sessions can do past the least (agent.Featured)
	Models  []agent.Model      `json:"models,omitempty"`
	Usage   core.ProviderUsage `json:"usage,omitzero"`
	Error   string             `json:"error,omitempty"`
}

// ProviderInfo is each provider as the launcher shows it: installed, logged in, its models, its usage. The
// providers are asked at once, so it takes as long as the slowest.
func (w *Host) ProviderInfo(ctx context.Context) []ProviderInfo {
	defer w.recover("ProviderInfo", nil)
	st := w.store.State()
	lost := w.run.LostLogins()
	ps := w.opts.Providers.List()
	out := make([]ProviderInfo, len(ps))
	var wg sync.WaitGroup
	for i, p := range ps {
		wg.Go(func() {
			defer w.recover("ProviderInfo", nil)
			info := ProviderInfo{ID: p.ID(), Has: agent.FeaturesOf(p)}
			dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			inst, err := p.Detect(dctx)
			cancel()
			info.Install = inst
			if err != nil {
				info.Error = err.Error()
			}
			for _, l := range lost {
				if l.Provider == p.ID() {
					info.Install.LoggedIn = false
					info.Install.Note = "Signed out: " + l.Detail
				}
			}
			if ms, err := p.Models(ctx); err == nil {
				info.Models = ms
			}
			for _, u := range st.Usage {
				if u.Provider == p.ID() {
					info.Usage = u
				}
			}
			out[i] = info
		})
	}
	wg.Wait()
	return out
}

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
		Sampled: w.opts.Now().UTC(), SignedOut: w.run.LostLogins()}
}

// SampleLoad reports the machine's load each time s says it moved, until ctx ends.
func (w *Host) SampleLoad(ctx context.Context, s *sysload.Sampler) {
	defer w.recover("SampleLoad", nil)
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.closed:
			return
		case r := <-s.Changes():
			func() {
				defer w.recover("SampleLoad", nil)
				_ = w.emit(core.MachineChanged{Machine: w.machineStateWith(r)})
			}()
		}
	}
}

// SetProviders sets where the host finds providers past its own (Options.Providers): the app's registry
// (registry.AgentProvider). The sessions handed over to a provider known only now are resumed, and every plan's
// windows read; nil takes them away.
func (w *Host) SetProviders(src func() []agent.Provider) {
	defer w.recover("SetProviders", nil)
	w.opts.Providers.SetSource(src)
	_ = w.emit(core.MachineChanged{Machine: w.machineState()})
	if src == nil {
		return
	}
	w.resumeHandedOver()
	w.env.Go("usage", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			select {
			case <-w.closed:
				cancel()
			case <-ctx.Done():
			}
		}()
		if err := w.ReadUsage(ctx); err != nil {
			w.log.Warn("usage", "err", err)
		}
	})
}

func (w *Host) resumeHandedOver() {
	w.run.ResumeHandedOver(func(id core.SessionID) error { return w.Resume(context.Background(), id) })
}
