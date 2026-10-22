package stats

import (
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/ui/widget"
)

// What the package's outside tests read of a plugin.

func (x *Plugin) Query() stats.Query                       { return x.query() }
func (x *Plugin) Report(q stats.Query) stats.Report        { r, _ := x.report(q); return r }
func (x *Plugin) Group() string                            { return x.q.group }
func (x *Plugin) Filters() map[string][]string             { return x.q.filters }
func (x *Plugin) Range() int                               { return x.q.rng }
func (x *Plugin) RangeDays() int                           { return statsRanges[x.q.rng].days }
func (x *Plugin) Selected() int                            { return x.sel }
func (x *Plugin) Split() bool                              { return x.split() }
func (x *Plugin) Offset() int                              { return x.offset() }
func (x *Plugin) Figure(span string) string                { return x.figure(span) }
func (x *Plugin) Reading() widget.LoadState                { return x.reading.State() }
func (x *Plugin) UsageAccount() (core.ProviderUsage, bool) { return x.usageAccount() }
func (x *Plugin) FrontUsage() (core.ProviderUsage, bool)   { return x.frontUsage() }
func (m *Machine) LoadOf() []float64                       { return m.loadOf() }

// ForgetReports drops every report read, so the next frame asks again.
func (x *Plugin) ForgetReports() {
	x.mu.Lock()
	defer x.mu.Unlock()
	clear(x.reports)
}

// FailDetail makes a session's stats a read that failed with err at at.
func (x *Plugin) FailDetail(id core.SessionID, err error, at time.Time) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.details[x.detailKey(id)] = fetched[stats.Detail]{at: at, err: err}
}

var Hours = hours

// Pinned says the HUD is pinned down.
func (x *HUD) Pinned() bool { return x.pinned }
