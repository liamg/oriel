package host

import (
	"context"
	"time"

	"github.com/liamg/oriel/agent/usage/stats"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/host/hostapi"
)

// StatsQuery answers a stats query over every session the host has.
func (w *Host) StatsQuery(q stats.Query) stats.Report {
	defer w.env.Recover("StatsQuery", nil)
	return w.Query(q)
}

// SessionStats is one session's stats.
func (w *Host) SessionStats(r hostapi.StatsSessionReq) (_ stats.Detail, err error) {
	defer w.env.Recover("SessionStats", &err)
	return w.Session(r)
}

// RefreshPrices keeps the price book fresh until ctx ends: cost.dev's list now, then once a day.
func (w *Host) RefreshPrices(ctx context.Context) {
	defer w.env.Recover("RefreshPrices", nil)
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	w.prices.Run(ctx, t.C)
}

// Logs is each session's log of notices, kept with the session.
func (w *Host) Logs() logs.SessionLogs { return w.Notices() }
