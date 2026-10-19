package host

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
)

// ProviderInfo is each provider as the launcher shows it: installed, logged in, its models, its usage. The
// providers are asked at once, so it takes as long as the slowest.
func (w *Host) ProviderInfo(ctx context.Context) []ProviderInfo {
	defer w.recover("ProviderInfo", nil)
	st := w.store.State()
	lost := w.lostLogins()
	ps := w.opts.Providers.List()
	out := make([]ProviderInfo, len(ps))
	var wg sync.WaitGroup
	for i, p := range ps {
		wg.Go(func() {
			defer w.recover("ProviderInfo", nil)
			info := ProviderInfo{ID: p.ID()}
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
func (w *Host) machineState() core.MachineState {
	host, _ := os.Hostname()
	r := w.opts.Load()
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
		Sampled: w.opts.Now().UTC(), SignedOut: w.lostLogins()}
}

// SampleLoad reports the machine's load every interval until ctx ends.
func (w *Host) SampleLoad(ctx context.Context, every time.Duration) {
	defer w.recover("SampleLoad", nil)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.closed:
			return
		case <-t.C:
			func() {
				defer w.recover("SampleLoad", nil)
				_ = w.emit(core.MachineChanged{Machine: w.machineState()})
			}()
		}
	}
}

// Terminal is the agent's raw terminal: what it has shown, then what it shows, until ctx ends or the agent
// stops.
func (w *Host) Terminal(ctx context.Context, id core.SessionID) (_ <-chan []byte, err error) {
	defer w.recover("Terminal", &err)
	h, _ := w.sessionFor(id, false)
	if h == nil {
		return nil, errors.New("the agent isn't running")
	}
	h.mu.Lock()
	sess := h.sess
	h.mu.Unlock()
	if sess == nil || sess.Terminal() == nil {
		return nil, errors.New("this agent has no terminal")
	}
	backlog, live, detach := sess.Terminal().Attach()
	out := make(chan []byte, 64)
	w.goSafe("Terminal", func() {
		defer close(out)
		defer detach()
		send := func(b []byte) bool {
			select {
			case out <- b:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if len(backlog) > 0 && !send(backlog) {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.closed:
				return
			case b, ok := <-live:
				if !ok || !send(b) {
					return
				}
			}
		}
	})
	return out, nil
}

// TermInput types into the agent's terminal, and resizes it.
func (w *Host) TermInput(r TermReq) (err error) {
	defer w.recover("TermInput", &err)
	h, _ := w.sessionFor(r.Session, false)
	if h == nil {
		return errors.New("the agent isn't running")
	}
	h.mu.Lock()
	sess := h.sess
	h.mu.Unlock()
	if sess == nil || sess.Terminal() == nil {
		return errors.New("this agent has no terminal")
	}
	t := sess.Terminal()
	if r.Cols > 0 && r.Rows > 0 {
		if err := t.Resize(r.Cols, r.Rows); err != nil {
			return err
		}
	}
	if len(r.Data) > 0 {
		_, err := t.Write(r.Data)
		return err
	}
	return nil
}
