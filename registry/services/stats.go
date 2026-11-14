package services

import (
	"github.com/liamg/oriel/agent/usage/stats"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// StatsService is the window's Stats.
const StatsService = "stats"

// Stats are what the sessions cost, as the Stats page last read them.
type Stats interface {
	// Session is a session's stats, once they've been read.
	Session(id core.SessionID) (stats.Detail, bool)
}

func StatsOf(r registry.Contributions) (Stats, bool) {
	return registry.ServiceOf[Stats](r, StatsService)
}
