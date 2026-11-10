package plugin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/liamg/oriel/ui"
)

// Loop is what a Load needs of its host: a way back to the app's loop, and a frame.
type Loop interface {
	Do(fn func())
	Redraw()
}

// Fetcher gets a Load's value off the loop. report says what it's doing and how far along it is (frac below 0 when
// that isn't known); it's safe to call from any goroutine.
type Fetcher[T any] func(ctx context.Context, report func(note string, frac float64)) (T, error)

type LoadOpts struct {
	Timeout time.Duration    // a fetch running longer fails; none when 0
	Now     func() time.Time // the clock loads are timed by; the window's (Host.UI) or time.Now when nil
	Name    string           // what it loads, named to a test waiting for it ("load" when empty)
}

// Load runs a fetch off the loop and tracks it for the widgets that draw it (ui.Async, ui.Busy). Its
// methods are called on the loop; a result lands there through Do. A result from a fetch started before the latest
// Reload or Reset is dropped, and a reload keeps the last value until the new one arrives.
type Load[T any] struct {
	h     Loop
	fetch Fetcher[T]
	opts  LoadOpts

	gen    int
	cancel context.CancelFunc
	val    T
	has    bool
	state  ui.LoadState
	busy   atomic.Bool // a fetch is running or its result hasn't landed; read from any goroutine
	end    func()      // stops the fetch counting as work under way (Begin)
}

func NewLoad[T any](h Loop, fetch Fetcher[T], o LoadOpts) *Load[T] {
	return &Load[T]{h: h, fetch: fetch, opts: o}
}

// Start fetches when nothing has been loaded or asked for yet; otherwise it does nothing, so a view may call it every
// frame.
func (l *Load[T]) Start() {
	if l.state.Phase == ui.Idle {
		l.Reload()
	}
}

// Reload starts a new fetch, dropping any running one, and keeps the current value on show while it runs.
func (l *Load[T]) Reload() {
	if l.cancel != nil {
		l.cancel()
	}
	l.gen++
	gen := l.gen
	ctx, cancel := context.WithCancel(context.Background())
	if l.opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), l.opts.Timeout)
	}
	l.cancel = cancel
	l.state = ui.LoadState{Phase: ui.Pending, Since: l.now(), Err: l.state.Err, Stale: l.has, Progress: -1}
	l.busy.Store(true)
	l.ended()
	l.end = Begin(l.h, cmp.Or(l.opts.Name, "load"))

	report := func(note string, frac float64) {
		l.h.Do(func() {
			if l.gen == gen && l.state.Phase == ui.Pending {
				l.state.Note, l.state.Progress = note, frac
			}
		})
		l.h.Redraw()
	}
	go func() {
		Chaos()
		v, err := l.run(ctx, report)
		cancel()
		l.h.Do(func() { l.land(gen, v, err) })
		l.h.Redraw()
	}()
}

// run is the fetch, ended by its context even if the fetch ignores it, and with a panic turned into an error.
func (l *Load[T]) run(ctx context.Context, report func(string, float64)) (T, error) {
	return runFetch(ctx, l.opts.Timeout, func(ctx context.Context) (T, error) { return l.fetch(ctx, report) })
}

// runFetch runs fetch until it returns or ctx ends, turning a panic into an error and a deadline into a timeout.
func runFetch[T any](ctx context.Context, timeout time.Duration, fetch func(context.Context) (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- result{err: fmt.Errorf("panic: %v", p)}
			}
		}()
		v, err := fetch(ctx)
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		if r.err != nil && errors.Is(r.err, context.DeadlineExceeded) {
			r.err = timedOut(timeout)
		}
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return zero, timedOut(timeout)
		}
		return zero, ctx.Err()
	}
}

func timedOut(d time.Duration) error {
	if d >= time.Second {
		d = d.Round(time.Second)
	}
	return fmt.Errorf("timed out after %s", d)
}

func (l *Load[T]) land(gen int, v T, err error) {
	if gen != l.gen {
		return
	}
	l.cancel = nil
	l.busy.Store(false)
	l.ended()
	l.state.Note, l.state.Progress, l.state.Stale = "", -1, false
	switch {
	case err == nil:
		l.val, l.has = v, true
		l.state.Phase, l.state.Err = ui.Ready, nil
	case l.has:
		l.state.Phase, l.state.Err = ui.Ready, err
	default:
		l.state.Phase, l.state.Err = ui.Failed, err
	}
}

// Reset forgets the value and cancels any fetch; the next Start fetches afresh.
func (l *Load[T]) Reset() {
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
	l.gen++
	l.busy.Store(false)
	l.ended()
	var zero T
	l.val, l.has = zero, false
	l.state = ui.LoadState{}
}

// Set makes v the value now, as a finished load would, dropping any fetch running.
func (l *Load[T]) Set(v T) {
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
	l.gen++
	l.busy.Store(false)
	l.ended()
	l.val, l.has = v, true
	l.state = ui.LoadState{Phase: ui.Ready, Progress: -1}
}

// Value is the last value loaded, and whether there's one.
func (l *Load[T]) Value() (T, bool) { return l.val, l.has }

func (l *Load[T]) State() ui.LoadState { return l.state }

// ended stops the fetch under way, if any, counting as work.
func (l *Load[T]) ended() {
	if l.end != nil {
		l.end()
		l.end = nil
	}
}

// Busy says a fetch is under way, until its result has landed on the loop. Unlike the rest, it's safe from any
// goroutine, for a registry.Busy.
func (l *Load[T]) Busy() bool { return l.busy.Load() }

func (l *Load[T]) now() time.Time { return loopNow(l.h, l.opts.Now) }

// loopNow is now(), else the window's clock when h has a window, else time.Now.
func loopNow(h Loop, now func() time.Time) time.Time {
	if now != nil {
		return now()
	}
	if h, ok := h.(interface{ UI() UIView }); ok {
		if u := h.UI(); u != nil {
			return u.Now()
		}
	}
	return time.Now()
}
