package stats

import (
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/watch"
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

// RefreshReports reads every report again.
func (x *Plugin) RefreshReports() {
	kit.EachHeld(&x.subs, func(_ string, sub watch.Sub[answer[stats.Report]]) { sub.Refresh() })
}

var Hours = hours

// Pinned says the HUD is pinned down.
func (x *HUD) Pinned() bool { return x.pinned }
