package host

import (
	"context"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
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
	defer w.env.Recover("ProviderInfo", nil)
	st := w.store.State()
	lost := w.runPart.LostLogins()
	ps := w.opts.Providers.List()
	out := make([]ProviderInfo, len(ps))
	var wg sync.WaitGroup
	for i, p := range ps {
		wg.Go(func() {
			defer w.env.Recover("ProviderInfo", nil)
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

// SetProviders sets where the host finds providers past its own (Options.Providers): the app's registry
// (services.AgentProvider). The sessions handed over to a provider known only now are resumed, and every plan's
// windows read; nil takes them away.
func (w *Host) SetProviders(src func() []agent.Provider) {
	defer w.env.Recover("SetProviders", nil)
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
		_ = w.ReadUsage(ctx) // each provider's failure is logged and kept in the state
	})
}
