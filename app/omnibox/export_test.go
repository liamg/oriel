package omnibox

import (
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry/uireg"
)

// What the package's outside tests read and set of its plugins.

type Group = group

const (
	PickerMax = pickerMax
	RankAway  = rankAway
)

// Source is the id of the source the group's results are of.
func (g group) Source() string { return g.src.ID }

// Items are the group's results, best first.
func (g group) Items() []uireg.PickerItem {
	out := make([]uireg.PickerItem, len(g.hits))
	for i, h := range g.hits {
		out[i] = h.item
	}
	return out
}

// RankSource is s's items for q, best first, at most limit of them, as a source of h's plugins ranks them.
func RankSource(h plugin.Host, s uireg.PickerSource, q string, limit int) (group, bool) {
	return rankSource(h, s, q, limit)
}

// SourceLoading says s is still loading its items.
func SourceLoading(s uireg.PickerSource) bool { return sourceLoading(s) }

func (o *Omnibox) Results(moving bool) []group { g, _ := o.results(moving); return g }
func (o *Omnibox) Open() bool                  { return o.open }

func (p *Picker) Open() bool     { return p.open }
func (p *Picker) Source() string { return p.source }
func (p *Picker) Query() string  { return p.query }
func (p *Picker) Selected() int  { return p.sel }

// Selected is the omnibox's selected row.
func (o *Omnibox) Selected() int { return o.sel }
