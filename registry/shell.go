package registry

import (
	"cmp"
	"slices"
	"strings"

	"github.com/liamg/oriel/ui"
)

// RailEntry is an icon on the rail down the window's left edge (ui.md 01f). It runs Command with Args, and is
// dimmed while Available says that wouldn't run.
type RailEntry struct {
	Name    string // unique among rail entries: "explorer"
	Icon    string // a glyph
	Title   string // the tooltip, with the key bound to Command and Args
	Command string
	Args    Args
	Badge   func() int // a count shown on the icon; nil or 0 for none
	Active  func() bool
	Order   int  // top to bottom
	Bottom  bool // pinned to the bottom, as Settings is
	Source  string
}

// StartEntry is an action on the start view (ui.md 01e), dimmed while its command isn't available.
type StartEntry struct {
	Name    string // unique among start entries: "session.new"
	Icon    string
	Title   string
	Desc    string
	Command string
	Args    Args
	Order   int
	Source  string
}

func (RailEntry) ContributionKind() string    { return "rail.entry" }
func (e RailEntry) ContributionName() string  { return e.Name }
func (StartEntry) ContributionKind() string   { return "start.entry" }
func (e StartEntry) ContributionName() string { return e.Name }

// RailEntries are the rail's entries, top ones then bottom ones, each by Order.
func RailEntries(r Registry) []RailEntry {
	var out []RailEntry
	for _, c := range r.Contributions("rail.entry") {
		out = append(out, c.(RailEntry))
	}
	slices.SortFunc(out, func(a, b RailEntry) int {
		if a.Bottom != b.Bottom {
			if a.Bottom {
				return 1
			}
			return -1
		}
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}

// StartEntries are the start view's actions by Order.
func StartEntries(r Registry) []StartEntry {
	var out []StartEntry
	for _, c := range r.Contributions("start.entry") {
		out = append(out, c.(StartEntry))
	}
	slices.SortFunc(out, func(a, b StartEntry) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}

// OverviewPanel is a panel on each card of the project overview (ui.md 01g): one card per repo or directory open.
// Compute runs off the app's loop, once per card and again when a repo's HEAD moves; the node it returns is drawn in
// the card, under Title.
type OverviewPanel struct {
	Name    string // unique among panels: "languages"
	Title   string
	Order   int
	Width   float32 // 0 shares the width the fixed ones leave
	Repos   bool    // on git repos' cards
	Dirs    bool    // on plain directories' cards
	Compute func(ctx Ctx, dir string) (ui.Node, error)
	Source  string
}

func (OverviewPanel) ContributionKind() string   { return "overview.panel" }
func (p OverviewPanel) ContributionName() string { return p.Name }

// OverviewPanels are the overview's panels by Order.
func OverviewPanels(r Registry) []OverviewPanel {
	var out []OverviewPanel
	for _, c := range r.Contributions("overview.panel") {
		out = append(out, c.(OverviewPanel))
	}
	slices.SortFunc(out, func(a, b OverviewPanel) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}

// KeyLayer is a keymap mode laid over every other while Active says so: a popup's keys, as the completion menu's
// <C-n> and <CR>. A key the layer doesn't map goes on as it would without it.
type KeyLayer struct {
	Name   string // unique among layers: "completion"
	Mode   string // the keymap mode its keys are in
	Active func() bool
	Source string
}

func (KeyLayer) ContributionKind() string   { return "key.layer" }
func (l KeyLayer) ContributionName() string { return l.Name }

// ActiveKeyModes are the modes of the layers active now, by name.
func ActiveKeyModes(r Registry) []string {
	var out []string
	for _, c := range r.Contributions("key.layer") {
		l := c.(KeyLayer)
		on := false
		Call(func() error { on = l.Active != nil && l.Active(); return nil })
		if on {
			out = append(out, l.Mode)
		}
	}
	return out
}
