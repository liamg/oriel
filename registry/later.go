package registry

import (
	"context"
	"sync"
	"sync/atomic"
)

// call is one Run that a caller waits on (OnDone): told once how it ended.
type call struct {
	done    func(error)
	started atomic.Bool // Run has claimed it; a Run under that one is part of the same call
	later   atomic.Bool // the command finishes after Run returns (Later)
	once    sync.Once
}

func (c *call) finish(err error) { c.once.Do(func() { c.done(err) }) }

type callKey struct{}

// OnDone is ctx for one Run whose caller is told, through done, how the command ended: as Run returns, or, for a
// command that finishes off the loop (Later), once it has, on the loop. done is called once.
func OnDone(ctx context.Context, done func(error)) Ctx {
	return context.WithValue(ctx, callKey{}, &call{done: done})
}

// Later is how a command whose slow part runs off the loop says it finishes after Run returns. It calls finish
// once, on the loop, with how it ended; waited says whether its caller asked to be told (OnDone), so a command
// nobody waits on reports its own failure.
func Later(ctx context.Context) (finish func(error), waited bool) {
	c, _ := ctx.Value(callKey{}).(*call)
	if c == nil {
		return func(error) {}, false
	}
	c.later.Store(true)
	return c.finish, true
}

// claim is the call Run reports to, when ctx is a caller's OnDone not yet run.
func claim(ctx context.Context) *call {
	c, _ := ctx.Value(callKey{}).(*call)
	if c == nil || c.started.Swap(true) {
		return nil
	}
	return c
}

// report tells c's caller how Run ended, unless the command finishes later and didn't fail.
func (c *call) report(err error) {
	if c != nil && (err != nil || !c.later.Load()) {
		c.finish(err)
	}
}
