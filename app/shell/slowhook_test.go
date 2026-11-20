package shell_test

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/registry"
)

// help plugins/hooks: a hook "over it on two calls running is a warning in the log". It's not a notice: there's
// nothing to do about a slow hook at the moment it's slow, and :Hooks marks it for when there is.
func TestASlowHookIsLoggedNotNotified(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Registry().Hook(registry.Hook{Event: "DirChanged", Source: "test:slow", Fn: func(registry.Ctx, any) (registry.Verdict, error) {
		s.SetNow(s.Now().Add(2 * apptest.HookBudget)) // each call takes twice the budget, on the scene's clock
		return registry.Verdict{}, nil
	}})
	for range 3 {
		s.Registry().Fire(context.Background(), "DirChanged", "", &kit.DirChanged{})
	}
	s.Quiet()
	if slices.ContainsFunc(s.Store().State().Notifications, func(n core.Notification) bool {
		return n.Source == "test:slow" || strings.Contains(n.Text, "budget")
	}) {
		t.Errorf("a slow hook made a notice: %+v", s.Store().State().Notifications)
	}
	if !slices.ContainsFunc(s.Logs.Ring.Records(), func(r logs.Record) bool {
		return r.Level == slog.LevelWarn && strings.Contains(r.Msg, "slow hook")
	}) {
		t.Errorf("the slow hook isn't in the log: %+v", s.Logs.Ring.Records())
	}
}
