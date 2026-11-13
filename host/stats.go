package host

import (
	"context"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/host/stats"
)

// StatsQuery answers a stats query over every session the host has.
func (w *Host) StatsQuery(q stats.Query) stats.Report {
	defer w.recover("StatsQuery", nil)
	return w.stats.Query(q)
}

// SessionStats is one session's stats.
func (w *Host) SessionStats(r StatsSessionReq) (_ stats.Detail, err error) {
	defer w.recover("SessionStats", &err)
	return w.stats.Session(r)
}

// UsageHistory is persist.Stats.UsageHistory, with a panic recovered.
func (w *Host) UsageHistory(r UsageHistoryReq) []core.ProviderUsage {
	defer w.recover("UsageHistory", nil)
	return w.stats.UsageHistory(r)
}

// ReadUsage is persist.Stats.ReadUsage, with a panic recovered.
func (w *Host) ReadUsage(ctx context.Context) (err error) {
	defer w.recover("ReadUsage", &err)
	return w.stats.ReadUsage(ctx)
}

// RefreshPrices keeps the price book fresh until ctx ends: cost.dev's list now, then once a day.
func (w *Host) RefreshPrices(ctx context.Context) {
	defer w.recover("RefreshPrices", nil)
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	w.prices.Run(ctx, t.C)
}

// Search finds text in sessions' threads and transcripts: archived ones keep theirs, so they stay searchable.
func (w *Host) Search(q SearchReq) (out []Hit) {
	defer w.recover("Search", nil)
	return w.logs.Search(q)
}

// Logs is each session's log of notices, kept with the session.
func (w *Host) Logs() logs.SessionLogs { return w.logs.Notices() }
