package plugin

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/ui/widget"
)

// loop is a host loop run by hand: Do queues, drain runs what's queued.
type loop struct {
	mu   sync.Mutex
	todo []func()
	poke chan struct{}
}

func newLoop() *loop { return &loop{poke: make(chan struct{}, 64)} }

func (l *loop) Do(fn func()) {
	l.mu.Lock()
	l.todo = append(l.todo, fn)
	l.mu.Unlock()
}

func (l *loop) Redraw() {
	select {
	case l.poke <- struct{}{}:
	default:
	}
}

func (l *loop) drain() {
	l.mu.Lock()
	fns := l.todo
	l.todo = nil
	l.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// until runs the loop until ok, failing after a second.
func (l *loop) until(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.After(time.Second)
	for !ok() {
		select {
		case <-l.poke:
			l.drain()
		case <-deadline:
			t.Fatal("timed out")
		}
	}
}

func TestLoadLifecycle(t *testing.T) {
	lp := newLoop()
	now := time.Unix(1000, 0)
	release := make(chan string)
	l := NewLoad(lp, func(ctx context.Context, report func(string, float64)) (string, error) {
		report("probing codex", .5)
		v := <-release
		if v == "" {
			return "", errors.New("no agents")
		}
		return v, nil
	}, LoadOpts{Now: func() time.Time { return now }})

	if s := l.State(); s.Phase != widget.Idle {
		t.Fatalf("before Start: %v", s.Phase)
	}
	l.Start()
	l.Start() // a second Start while one runs does nothing
	if s := l.State(); s.Phase != widget.Pending || !s.Since.Equal(now) || s.Stale {
		t.Fatalf("after Start: %+v", s)
	}
	lp.until(t, func() bool { return l.State().Note != "" })
	if s := l.State(); s.Note != "probing codex" || s.Progress != .5 {
		t.Errorf("the report: %+v", s)
	}
	release <- ""
	lp.until(t, func() bool { return l.State().Phase != widget.Pending })
	if s := l.State(); s.Phase != widget.Failed || s.Err == nil || s.Err.Error() != "no agents" {
		t.Fatalf("a failed first load: %+v", s)
	}

	l.Reload()
	release <- "claude"
	lp.until(t, func() bool { return l.State().Phase == widget.Ready })
	if v, ok := l.Value(); !ok || v != "claude" || l.State().Err != nil {
		t.Fatalf("after Retry: %q %v %+v", v, ok, l.State())
	}

	l.Reload()
	if s := l.State(); s.Phase != widget.Pending || !s.Stale {
		t.Errorf("a reload keeps the value on show: %+v", s)
	}
	if v, _ := l.Value(); v != "claude" {
		t.Errorf("value while reloading: %q", v)
	}
	release <- ""
	lp.until(t, func() bool { return l.State().Phase != widget.Pending })
	if s := l.State(); s.Phase != widget.Ready || s.Err == nil {
		t.Errorf("a failed reload keeps the value and shows the error: %+v", s)
	}
	if v, _ := l.Value(); v != "claude" {
		t.Errorf("value after a failed reload: %q", v)
	}
}

// A result from before a Reset or Reload never lands.
func TestLoadDropsStaleResults(t *testing.T) {
	lp := newLoop()
	calls := make(chan chan int, 2)
	l := NewLoad(lp, func(ctx context.Context, _ func(string, float64)) (int, error) {
		ch := make(chan int)
		calls <- ch
		return <-ch, nil
	}, LoadOpts{})
	l.Start()
	first := <-calls
	l.Reload()
	second := <-calls
	second <- 2
	lp.until(t, func() bool { return l.State().Phase == widget.Ready })
	first <- 1
	time.Sleep(20 * time.Millisecond)
	lp.drain()
	if v, _ := l.Value(); v != 2 {
		t.Errorf("value %d: the older fetch's result landed", v)
	}

	l.Reset()
	if _, ok := l.Value(); ok || l.State().Phase != widget.Idle {
		t.Errorf("after Reset: %+v", l.State())
	}
}

// A fetch that never returns times out, and a panic fails the load.
func TestLoadTimeoutAndPanic(t *testing.T) {
	lp := newLoop()
	hang := NewLoad(lp, func(ctx context.Context, _ func(string, float64)) (int, error) {
		select {}
	}, LoadOpts{Timeout: 20 * time.Millisecond})
	hang.Start()
	lp.until(t, func() bool { return hang.State().Phase == widget.Failed })
	if err := hang.State().Err; err == nil || err.Error() != "timed out after 20ms" {
		t.Errorf("timeout: %v", err)
	}

	boom := NewLoad(lp, func(ctx context.Context, _ func(string, float64)) (int, error) {
		panic("boom")
	}, LoadOpts{})
	boom.Start()
	lp.until(t, func() bool { return boom.State().Phase == widget.Failed })
	if err := boom.State().Err; err == nil || err.Error() != "panic: boom" {
		t.Errorf("panic: %v", err)
	}
}
