package stats

import (
	"github.com/liamg/oriel/agent/usage/stats"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/watch"
)

// What the package's outside tests read of a plugin.

func (x *Stats) Query() stats.Query                       { return x.query() }
func (x *Stats) Report(q stats.Query) stats.Report        { r, _ := x.report(q); return r }
func (x *Stats) Group() string                            { return x.q.group }
func (x *Stats) Filters() map[string][]string             { return x.q.filters }
func (x *Stats) Range() int                               { return x.q.rng }
func (x *Stats) RangeDays() int                           { return statsRanges[x.q.rng].days }
func (x *Stats) Selected() int                            { return x.sel }
func (x *Stats) Split() bool                              { return x.split() }
func (x *Stats) Offset() int                              { return x.offset() }
func (x *Stats) Figure(span string) string                { return x.figure(span) }
func (x *Stats) Reading() widget.LoadState                { return x.reading.State() }
func (x *Stats) UsageAccount() (core.ProviderUsage, bool) { return x.usageAccount() }
func (x *Stats) FrontUsage() (core.ProviderUsage, bool)   { return x.frontUsage() }
func (m *Machine) LoadOf() []float64                      { return m.loadOf() }

// RefreshReports reads every report again.
func (x *Stats) RefreshReports() {
	kit.EachHeld(&x.subs, func(_ string, sub watch.Sub[answer[stats.Report]]) { sub.Refresh() })
}

var Hours = hours

// Pinned says the HUD is pinned down.
func (x *HUD) Pinned() bool { return x.pinned }
