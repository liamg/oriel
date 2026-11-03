package host

import (
	"context"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/sandboxhost"
	"github.com/liamg/oriel/host/stats"
)

// What the tests reach in the host's parts, by the names they had in the host.

func (w *Host) addRepo(ctx context.Context, path string, src core.RepoSource) (core.Repo, error) {
	return w.repos.Add(ctx, path, src)
}

func (w *Host) resolveRepo(ctx context.Context, ref string) (core.Repo, error) {
	return w.repos.Resolve(ctx, ref)
}

func (w *Host) sessionDir(id core.SessionID) string { return w.env.SessionDir(id) }

func (w *Host) usageSampled(u core.ProviderUsage) { w.stats.UsageSampled(u) }

func (w *Host) statsStore() *stats.Store { return w.stats.Store() }

func (w *Host) statsSessions(only core.SessionID, since time.Time) []stats.Session {
	return w.stats.Sessions(only, since)
}

func (w *Host) addRule(id core.SessionID, rule string) error {
	return w.emit(core.RuleAdded{Session: id, Rule: rule})
}

// boxOf is session id's sandbox while its agent runs in one.
func (w *Host) boxOf(id core.SessionID) *sandboxhost.Box { return sandboxhost.Of(w.run.Sandbox(id)) }
