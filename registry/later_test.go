package registry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/liamg/oriel/registry"
)

// waiter counts how a Run's caller is told it ended.
type waiter struct {
	calls int
	err   error
}

func (w *waiter) ctx() registry.Ctx {
	return registry.OnDone(context.Background(), func(err error) { w.calls++; w.err = err })
}

func TestOnDoneHearsACommandThatEndsAsItReturns(t *testing.T) {
	r := registry.New()
	boom := errors.New("boom")
	r.Command(registry.Command{ID: "x.ok", Run: func(registry.Ctx, registry.Args) error { return nil }})
	r.Command(registry.Command{ID: "x.fail", Run: func(registry.Ctx, registry.Args) error { return boom }})
	var ok, failed, missing waiter
	r.Run(ok.ctx(), "x.ok", nil)
	r.Run(failed.ctx(), "x.fail", nil)
	r.Run(missing.ctx(), "x.none", nil)
	if ok.calls != 1 || ok.err != nil {
		t.Errorf("x.ok: told %d times, err %v", ok.calls, ok.err)
	}
	if failed.calls != 1 || !errors.Is(failed.err, boom) {
		t.Errorf("x.fail: told %d times, err %v", failed.calls, failed.err)
	}
	if missing.calls != 1 || missing.err == nil {
		t.Errorf("a missing command: told %d times, err %v", missing.calls, missing.err)
	}
}

func TestOnDoneWaitsForACommandThatFinishesLater(t *testing.T) {
	r := registry.New()
	var finish func(error)
	var waited bool
	r.Command(registry.Command{ID: "x.slow", Run: func(ctx registry.Ctx, _ registry.Args) error {
		finish, waited = registry.Later(ctx)
		return nil
	}})
	r.Command(registry.Command{ID: "x.outer", Run: func(ctx registry.Ctx, _ registry.Args) error {
		return r.Run(ctx, "x.slow", nil)
	}})
	for _, id := range []string{"x.slow", "x.outer"} {
		var w waiter
		if err := r.Run(w.ctx(), id, nil); err != nil {
			t.Fatal(err)
		}
		if !waited || w.calls != 0 {
			t.Fatalf("%s: waited %v, told %d times before it finished", id, waited, w.calls)
		}
		late := errors.New("late")
		finish(late)
		finish(nil)
		if w.calls != 1 || !errors.Is(w.err, late) {
			t.Errorf("%s: told %d times, err %v", id, w.calls, w.err)
		}
	}
}

func TestLaterWithNobodyWaiting(t *testing.T) {
	r := registry.New()
	waited := true
	r.Command(registry.Command{ID: "x.slow", Run: func(ctx registry.Ctx, _ registry.Args) error {
		var finish func(error)
		finish, waited = registry.Later(ctx)
		finish(nil)
		return nil
	}})
	r.Run(context.Background(), "x.slow", nil)
	if waited {
		t.Error("Later says a caller waits when none asked")
	}
}
