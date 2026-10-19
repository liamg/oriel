package plugin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liamg/oriel/ui/widget"
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
}

// Load runs a fetch off the loop and tracks it for the widgets that draw it (widget.Async, widget.Busy). Its
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
	state  widget.LoadState
}

func NewLoad[T any](h Loop, fetch Fetcher[T], o LoadOpts) *Load[T] {
	return &Load[T]{h: h, fetch: fetch, opts: o}
}

// Start fetches when nothing has been loaded or asked for yet; otherwise it does nothing, so a view may call it every
// frame.
func (l *Load[T]) Start() {
	if l.state.Phase == widget.Idle {
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
	l.state = widget.LoadState{Phase: widget.Pending, Since: l.now(), Err: l.state.Err, Stale: l.has, Progress: -1}

	report := func(note string, frac float64) {
		l.h.Do(func() {
			if l.gen == gen && l.state.Phase == widget.Pending {
				l.state.Note, l.state.Progress = note, frac
			}
		})
		l.h.Redraw()
	}
	go func() {
		v, err := l.run(ctx, report)
		cancel()
		l.h.Do(func() { l.land(gen, v, err) })
		l.h.Redraw()
	}()
}

// run is the fetch, ended by its context even if the fetch ignores it, and with a panic turned into an error.
func (l *Load[T]) run(ctx context.Context, report func(string, float64)) (T, error) {
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
		v, err := l.fetch(ctx, report)
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		if r.err != nil && errors.Is(r.err, context.DeadlineExceeded) {
			r.err = l.timedOut()
		}
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return zero, l.timedOut()
		}
		return zero, ctx.Err()
	}
}

func (l *Load[T]) timedOut() error {
	d := l.opts.Timeout
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
	l.state.Note, l.state.Progress, l.state.Stale = "", -1, false
	switch {
	case err == nil:
		l.val, l.has = v, true
		l.state.Phase, l.state.Err = widget.Ready, nil
	case l.has:
		l.state.Phase, l.state.Err = widget.Ready, err
	default:
		l.state.Phase, l.state.Err = widget.Failed, err
	}
}

// Reset forgets the value and cancels any fetch; the next Start fetches afresh.
func (l *Load[T]) Reset() {
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
	l.gen++
	var zero T
	l.val, l.has = zero, false
	l.state = widget.LoadState{}
}

// Set makes v the value now, as a finished load would, dropping any fetch running.
func (l *Load[T]) Set(v T) {
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
	l.gen++
	l.val, l.has = v, true
	l.state = widget.LoadState{Phase: widget.Ready, Progress: -1}
}

// Value is the last value loaded, and whether there's one.
func (l *Load[T]) Value() (T, bool) { return l.val, l.has }

func (l *Load[T]) State() widget.LoadState { return l.state }

func (l *Load[T]) now() time.Time {
	if l.opts.Now != nil {
		return l.opts.Now()
	}
	if h, ok := l.h.(interface{ UI() UIView }); ok {
		if u := h.UI(); u != nil {
			return u.Now()
		}
	}
	return time.Now()
}
