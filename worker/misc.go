package worker

import (
	"context"
	"errors"
	"os"
	"runtime"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/worker/sysload"
)

// ProviderInfo is each provider as the launcher shows it: installed, logged in, its models, its usage.
func (w *Worker) ProviderInfo(ctx context.Context) []ProviderInfo {
	var out []ProviderInfo
	st := w.store.State()
	for _, p := range w.opts.Providers.List() {
		info := ProviderInfo{ID: p.ID()}
		dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		inst, err := p.Detect(dctx)
		cancel()
		info.Install = inst
		if err != nil {
			info.Error = err.Error()
		}
		for _, l := range w.lostLogins() {
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
		out = append(out, info)
	}
	return out
}

// workerState is this worker as the app sees it, with a fresh reading of its load.
func (w *Worker) workerState() core.WorkerState {
	host, _ := os.Hostname()
	r := sysload.Read()
	return core.WorkerState{ID: w.opts.ID, Name: w.opts.Name, Host: host, OS: runtime.GOOS, Connected: true,
		Cores: r.Cores, CPU: r.CPU / 100, Mem: r.MemUsed, MemTotal: r.MemTotal, Sampled: w.opts.Now().UTC(), SignedOut: w.lostLogins()}
}

// SampleLoad reports the worker's load every interval until ctx ends.
func (w *Worker) SampleLoad(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.closed:
			return
		case <-t.C:
			_ = w.emit(core.WorkerChanged{Worker: w.workerState()})
		}
	}
}

// serveTerminal streams the agent's raw terminal: what it has shown, then what it shows.
func (w *Worker) serveTerminal(ctx context.Context, r SessionRef, send func(any) error) error {
	h, _ := w.hostFor(r.Session, false)
	if h == nil {
		return errors.New("the agent isn't running")
	}
	h.mu.Lock()
	sess := h.sess
	h.mu.Unlock()
	if sess == nil || sess.Terminal() == nil {
		return errors.New("this agent has no terminal")
	}
	backlog, live, detach := sess.Terminal().Attach()
	defer detach()
	if len(backlog) > 0 {
		if err := send(backlog); err != nil {
			return err
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case b, ok := <-live:
			if !ok {
				return nil
			}
			if err := send(b); err != nil {
				return err
			}
		}
	}
}

func (w *Worker) termInput(r TermReq) error {
	h, _ := w.hostFor(r.Session, false)
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
