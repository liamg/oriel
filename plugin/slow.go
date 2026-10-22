package plugin

import (
	"context"
	"errors"
	"fmt"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// Slow is what a command does off the loop: a git fetch, a clone, a worktree made.
type Slow struct {
	// ID names the work: its notice's id, and while it runs another Slow with the same ID in the window is refused.
	ID string
	// Doing says what it's doing, for the notice up while it runs: "Fetching oriel".
	Doing string
}

// ErrBusy is a Slow refused because the same work is running.
var ErrBusy = errors.New("already under way")

// RunSlow is a command's slow part: work runs off the loop, then land, if any, runs on the loop with what work made.
// The command returns at once and finishes later (registry.Later). A caller that waits (registry.OnDone) is told how
// it ended and shows its own busy state; for one that doesn't, a notice says what it's doing, and then any failure.
func RunSlow[T any](ctx registry.Ctx, h Host, s Slow, work func(context.Context) (T, error), land func(registry.Ctx, T) error) error {
	release, ok := h.Claim("slow:" + s.ID)
	if !ok {
		return fmt.Errorf("%s: %w", s.Doing, ErrBusy)
	}
	finish, waited := registry.Later(ctx)
	if !waited {
		h.Notify(core.Notice{ID: s.ID, Severity: core.SevInfo, Text: s.Doing + "…"})
	}
	bg := context.WithoutCancel(ctx)
	go func() {
		v, err := guarded(func() (T, error) { return work(bg) })
		h.Do(func() {
			release()
			if err == nil && land != nil {
				err = registry.Call(func() error { return land(bg, v) })
			}
			switch {
			case waited:
			case err != nil:
				h.Notify(core.Notice{ID: s.ID, Severity: core.SevError, Text: err.Error()})
			default:
				h.Dismiss(s.ID)
			}
			finish(err)
		})
	}()
	return nil
}

// guarded is fn with a panic turned into an error.
func guarded[T any](fn func() (T, error)) (v T, err error) {
	err = registry.Call(func() error {
		var ferr error
		v, ferr = fn()
		return ferr
	})
	return v, err
}

// Await calls run with a ctx to run one command with, and waits until that command has finished, a slow part
// (RunSlow) too, or ctx ends: off the loop, where a slow part lands.
func Await(ctx context.Context, run func(registry.Ctx) error) error {
	done := make(chan error, 1)
	if err := run(registry.OnDone(ctx, func(err error) { done <- err })); err != nil {
		select {
		case derr := <-done:
			return derr
		default:
			return err
		}
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
