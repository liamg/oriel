package plugin

import (
	"context"
	"sync"
)

type teardownKey struct{}

// Teardowns is the slow cleanup of plugins a window has stopped: a language server's shutdown, waits for git. It
// runs off the loop while the window goes on, and the window waits for it as it closes.
type Teardowns struct{ wg sync.WaitGroup }

// WithTeardowns is ctx for Deactivate, with its Teardown calls running on t.
func WithTeardowns(ctx context.Context, t *Teardowns) context.Context {
	return context.WithValue(ctx, teardownKey{}, t)
}

// Wait waits for the cleanup running now.
func (t *Teardowns) Wait() { t.wg.Wait() }

// Teardown is the slow part of a plugin's Deactivate, given its ctx: off the loop when the window goes on to
// another session, and done before it returns otherwise (the app quitting).
func Teardown(ctx context.Context, fn func(context.Context)) {
	t, ok := ctx.Value(teardownKey{}).(*Teardowns)
	if !ok {
		fn(ctx)
		return
	}
	bg := context.WithoutCancel(ctx)
	t.wg.Go(func() { fn(bg) })
}
