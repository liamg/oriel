package watch

import (
	"slices"
	"strings"
	"time"
)

// SourceStat is a source as the Performance page lists it.
type SourceStat struct {
	Key      string
	State    string // "reading", "watching", "polling", "paused", or "idle" in its grace
	Need     Need   // the tightest active need
	Subs     []SubStat
	LastRead time.Time
	Reads    int // since it was first read
	PerMin   int // reads begun in the last minute
	Err      error
	Stale    bool   // changed since its last read began
	Cause    string // what made it read last: a changed path, a source it reads from, "refresh" or "poll"
}

// SubStat is one subscription to a source.
type SubStat struct {
	Label  string // who asked; a source's own hold on one it reads from is that source's key
	Need   Need
	Paused bool
	Held   bool // a source's hold, not a subscriber's
}

// Stats are the manager's sources, by key.
func (m *Manager) Stats() []SourceStat {
	m.mu.Lock()
	defer m.mu.Unlock()
	cut := m.opts.Clock.Now().Add(-time.Minute)
	var out []SourceStat
	for _, n := range m.nodes {
		st := SourceStat{Key: n.spec.key, Need: n.need, LastRead: n.at, Reads: n.reads, Err: n.err, Stale: n.dirty,
			Cause: n.readCause}
		for _, t := range n.times {
			if t.After(cut) {
				st.PerMin++
			}
		}
		switch {
		case n.reading:
			st.State = "reading"
		case n.active && (n.spec.poll || n.fallback):
			st.State = "polling"
		case n.active:
			st.State = "watching"
		case n.paused:
			st.State = "paused"
		default:
			st.State = "idle"
		}
		for s := range n.subs {
			if !s.closed {
				st.Subs = append(st.Subs, SubStat{Label: s.label, Need: s.need, Paused: s.paused, Held: s.internal})
			}
		}
		slices.SortFunc(st.Subs, func(a, b SubStat) int { return strings.Compare(a.Label, b.Label) })
		out = append(out, st)
	}
	slices.SortFunc(out, func(a, b SourceStat) int { return strings.Compare(a.Key, b.Key) })
	return out
}
