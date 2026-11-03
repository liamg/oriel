package kit

import (
	"context"
	"sync"
	"time"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
)

// OnLoop runs fn on the app's loop and waits for it: an agent tool's way in from the session host's goroutine.
func OnLoop(ctx context.Context, h plugin.Host, fn func()) error {
	done := make(chan struct{})
	h.Do(func() { fn(); close(done) })
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Disposer is the usual way for a plugin to undo what it registered.
type Disposer []registry.Disposable

func (d *Disposer) Add(x registry.Disposable) { *d = append(*d, x) }

// OnDispose is a func as a Disposable: a subscription's cancel.
type OnDispose func()

func (f OnDispose) Dispose() { f() }

// Lifetime is a context that ends when d is disposed: for what a plugin waits on off the loop.
func (d *Disposer) Lifetime() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	d.Add(OnDispose(cancel))
	return ctx
}

func (d *Disposer) Deactivate(context.Context) error {
	for _, x := range *d {
		x.Dispose()
	}
	*d = nil
	return nil
}

// PagePlace is a page shown while *open, as a place in the window's history.
func PagePlace(name, title, command string, open *bool) uireg.Place {
	return uireg.Place{Name: name, Title: title, Command: command, Shown: func() bool { return *open }, Close: func() { *open = false }}
}

// PollSet is the periods of a window's periodic work while it runs (PollsOf). A nil one notes nothing.
type PollSet struct {
	mu      sync.Mutex
	live    map[int]time.Duration
	next    int
	fastest time.Duration // since the last reset
}

// Start notes work running every d until stop.
func (p *PollSet) Start(d time.Duration) (stop func()) {
	if p == nil {
		return func() {}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.live == nil {
		p.live = map[int]time.Duration{}
	}
	id := p.next
	p.next++
	p.live[id] = d
	if p.fastest == 0 || d < p.fastest {
		p.fastest = d
	}
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		delete(p.live, id)
	}
}

// Reset forgets all but what's running now.
func (p *PollSet) Reset() {
	f := p.FastestNow()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fastest = f
}

// FastestSince is the shortest period running since the last reset, or 0.
func (p *PollSet) FastestSince() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fastest
}

// Ticker is a time.Ticker noted in polls.
type Ticker struct {
	*time.Ticker
	stop func()
}

// NewTicker is a ticker every d, noted in polls.
func NewTicker(polls *PollSet, d time.Duration) *Ticker {
	return &Ticker{time.NewTicker(d), polls.Start(d)}
}

func (t *Ticker) Stop() {
	t.Ticker.Stop()
	t.stop()
}

// FastestNow is the shortest period running now, or 0.
func (p *PollSet) FastestNow() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	var f time.Duration
	for _, d := range p.live {
		if f == 0 || d < f {
			f = d
		}
	}
	return f
}

// Signals are funcs called together each time something happens.
type Signals struct {
	mu   sync.Mutex
	fns  map[int]func()
	next int
}

// On calls fn each time, until cancelled.
func (s *Signals) On(fn func()) (cancel OnDispose) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fns == nil {
		s.fns = map[int]func(){}
	}
	id := s.next
	s.next++
	s.fns[id] = fn
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.fns, id)
	}
}

func (s *Signals) Fire() {
	s.mu.Lock()
	fns := make([]func(), 0, len(s.fns))
	for _, fn := range s.fns {
		fns = append(fns, fn)
	}
	s.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// WhileShown holds what a node wants for as long as it's drawn with the window in focus: its build calls keep each
// frame, and a frame drawn without it, or with the window behind another app, lets go.
type WhileShown struct {
	Want  func() (release func())
	Focus interface {
		WindowFocus() (bool, <-chan struct{})
	} // the window, whose focus it's kept with
	release func()
	kept    bool // since the last frame
}

// Keep is called from the build of a node that wants it.
func (w *WhileShown) Keep() {
	if on, _ := w.Focus.WindowFocus(); !on {
		w.Drop()
		return
	}
	w.kept = true
	if w.release == nil {
		w.release = w.Want()
	}
}

// Framed lets go if no build kept it this frame.
func (w *WhileShown) Framed() {
	if !w.kept {
		w.Drop()
	}
	w.kept = false
}

func (w *WhileShown) Drop() {
	if w.release != nil {
		w.release()
		w.release = nil
	}
}
