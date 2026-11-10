package kit

import (
	"context"
	"sync"
)

// Work is a plugin's goroutines: each gets a ctx that ends when Stop is called, and Stop waits for them all, so a
// plugin that's stopped leaves nothing running. The zero Work is ready to use.
type Work struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	wg      Busy           // the tasks
	loops   sync.WaitGroup // what runs as long as the plugin: not busy, only stopped
	stopped bool
}

func (w *Work) init() {
	if w.ctx == nil {
		w.ctx, w.cancel = context.WithCancel(context.Background())
	}
}

// Ctx ends when Stop is called.
func (w *Work) Ctx() context.Context {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.init()
	return w.ctx
}

// Go runs fn on a goroutine of its own, unless Stop has been called.
func (w *Work) Go(fn func(ctx context.Context)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	w.init()
	ctx := w.ctx
	w.wg.Go(func() { fn(ctx) })
}

// Loop runs fn on a goroutine of its own for as long as it likes, unless Stop has been called: a ticker, a wait on
// something that ends when it ends. Unlike Go's, it doesn't count as work under way (Busy). It says whether fn runs.
func (w *Work) Loop(fn func(ctx context.Context)) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return false
	}
	w.init()
	ctx := w.ctx
	w.loops.Go(func() { fn(ctx) })
	return true
}

// Stop ends every goroutine's ctx and waits for them to return.
func (w *Work) Stop() {
	w.mu.Lock()
	w.init()
	w.stopped = true
	w.cancel()
	w.mu.Unlock()
	w.wg.Wait()
	w.loops.Wait()
}

// Wait waits for the goroutines running now to return, without stopping: a test settling.
func (w *Work) Wait() { w.wg.Wait() }

// Busy says a task (Go's) is running.
func (w *Work) Busy() bool { return w.wg.Busy() }

// Dispose is Stop, for a Disposer.
func (w *Work) Dispose() { w.Stop() }
