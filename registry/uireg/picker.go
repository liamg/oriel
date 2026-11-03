package uireg

import (
	"cmp"
	"slices"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

// PickerItem is one result a source offers. Label is what the query matches and the row shows.
type PickerItem struct {
	ID         string
	Label      string
	Detail     string // after the label, dim: "work · ENG-2231"
	Desc       string // matched, but not in the row: for the preview to show in full
	Note       string // at the right: "replied 6m"
	Icon       string // a glyph before the label; empty for a dot in IconStyle
	IconStyle  string // a theme token
	Badge      string // a chip at the right: "TODO", "REVIEW REQUESTED"
	BadgeStyle string
	Key        string // a shortcut shown at the far right: "⌘2"
	Boost      int    // added to the match score: recently used, needs you
	Path       string // a file, for an item that's a place: then open, split, quickfix and attach work on it
	Line, Col  int    // 1-based; 0 for the file's start
	Data       any    // the source's own, for its Preview and Run
	Current    bool   // the one in use now (the current theme): the picker opens on it
}

// PickerAction is something a picker does to the selected items (or the highlighted one) on a key.
type PickerAction struct {
	Key   string // in vim notation: "<C-e>"
	Title string
	Run   func(ctx registry.Ctx, items []PickerItem) error
}

// PickerSource is a source of results for the omnibox and the pickers: sessions, commands, files, symbols, and
// whatever plugins add (tickets, PRs, themes). Prefix narrows the omnibox to it: "@" files, ">" commands.
type PickerSource struct {
	ID      string
	Title   string // "Sessions", "Files"
	Prefix  string
	Order   int // where it sits in the omnibox's merged list, lowest first
	Items   func(ctx registry.Ctx, query string) []PickerItem
	Preview func(item PickerItem) ui.Node // nil for none
	Run     func(ctx registry.Ctx, item PickerItem) error
	// Hover is called as an item becomes the highlighted one, to show it live (a theme); Cancel when the
	// picker closes, or moves to another source, without running one, to put things back.
	Hover   func(item PickerItem)
	Cancel  func()
	Actions []PickerAction // beyond the ones every item with a Path has
	// Live sources match the query themselves, as grep does: their items are the results, not candidates.
	Live       bool
	PickerOnly bool // in the pickers, not the omnibox's merged list
	Unlisted   bool // opened by its command only, not in the pickers' strip or ⇥: a result list, such as callers
	// Available says whether it has anything to offer now; one that hasn't is left out of Pickers. Nil is always.
	Available func() bool
	// Key, when set, says what Items are made from, cheaply and comparably (a generation, a pointer): its items are
	// asked for again only when it changes. A source whose items depend on the query, or on what it can't key,
	// leaves it nil and is asked again as the query changes, and every so often.
	Key func() any
	// Loading says its items are still coming (a walk, a search, a server's answer): the picker shows it's busy,
	// and says there are no matches only once it isn't. Nil is never.
	Loading func() bool
	Source  string
}

func (PickerSource) ContributionKind() string   { return "picker" }
func (s PickerSource) ContributionName() string { return s.ID }

func (r *reg) Picker(s PickerSource) registry.Disposable { return r.Contribute(s) }

func (r *reg) Pickers() []PickerSource {
	var out []PickerSource
	for _, c := range r.Contributions("picker") {
		s := c.(PickerSource)
		ok := s.Available == nil
		if !ok {
			registry.Call(func() error { ok = s.Available(); return nil })
		}
		if ok {
			out = append(out, s)
		}
	}
	for _, c := range r.Contributions("placeholder.omnibox") {
		out = append(out, r.holding(c.(registry.OmniboxHolder)))
	}
	slices.SortFunc(out, func(a, b PickerSource) int { return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.ID, b.ID)) })
	return out
}

// holding is the source standing for a placeholder's omnibox prefix: its first use provides what the placeholder
// stands for, and passes on to the source with the prefix that came.
func (r *reg) holding(h registry.OmniboxHolder) PickerSource {
	provided := func() (PickerSource, bool) {
		if h.Provide() != nil {
			return PickerSource{}, false
		}
		for _, s := range r.Pickers() {
			if s.Prefix == h.Prefix {
				return s, true
			}
		}
		return PickerSource{}, false
	}
	return PickerSource{ID: h.ContributionName(), Title: h.Name, Prefix: h.Prefix, Live: true, PickerOnly: true, Unlisted: true,
		Source: h.Source,
		Items: func(ctx registry.Ctx, q string) []PickerItem {
			if s, ok := provided(); ok && s.Items != nil {
				return s.Items(ctx, q)
			}
			return nil
		},
		Run: func(ctx registry.Ctx, it PickerItem) error {
			if s, ok := provided(); ok && s.Run != nil {
				return s.Run(ctx, it)
			}
			return nil
		},
		Preview: func(it PickerItem) ui.Node {
			if s, ok := provided(); ok && s.Preview != nil {
				return s.Preview(it)
			}
			return nil
		}}
}
