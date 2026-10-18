package worker

import (
	"context"
	"errors"
	"time"

	"github.com/liamg/oriel/core"
)

// KeyWait is how long a git operation that signs, or reaches a remote over ssh, runs before it's taken to be waiting
// on a security-key touch (crit's five seconds: less is a slow network).
const KeyWait = 5 * time.Second

// ErrKeyCancelled is a git operation stopped from its security-key card.
var ErrKeyCancelled = errors.New("cancelled while it waited for a security-key touch")

// keyed runs fn, a git operation of session id's that may wait on a security-key touch: once it has run for
// Options.KeyWait, the session asks for the touch (a core.AskKey) until it ends, and Cancel there stops it.
func (w *Worker) keyed(ctx context.Context, id core.SessionID, what, project string, fn func(ctx context.Context) error) error {
	wait := w.opts.KeyWait
	if wait == 0 {
		wait = KeyWait
	}
	if wait < 0 {
		return fn(ctx)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer w.recover("a key wait", nil)
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-done:
			return
		case <-t.C:
		}
		askCtx, stop := context.WithCancel(context.Background())
		go func() {
			<-done
			stop()
		}()
		a, ok := w.hostOf(id).askUser(askCtx, core.Ask{Kind: core.AskKey, Summary: what, Detail: project})
		if ok && a.Decision == "deny" {
			cancel(ErrKeyCancelled)
		}
	}()
	err := fn(ctx)
	if cause := context.Cause(ctx); errors.Is(cause, ErrKeyCancelled) {
		return cause
	}
	return err
}
