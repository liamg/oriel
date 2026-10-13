package registry

import (
	"cmp"
	"slices"

	"github.com/liamg/oriel/ui"
)

// PickerItem is one result a source offers. Label is what the query matches and the row shows.
type PickerItem struct {
	ID         string
	Label      string
	Detail     string // after the label, dim: "work · INF-2231"
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
	Run   func(ctx Ctx, items []PickerItem) error
}

// PickerSource is a source of results for the omnibox and the pickers: sessions, commands, files, symbols, and
// whatever plugins add (tickets, PRs, themes). Prefix narrows the omnibox to it: "@" files, ">" commands.
type PickerSource struct {
	ID      string
	Title   string // "Sessions", "Files"
	Prefix  string
	Order   int // where it sits in the omnibox's merged list, lowest first
	Items   func(ctx Ctx, query string) []PickerItem
	Preview func(item PickerItem) ui.Node // nil for none
	Run     func(ctx Ctx, item PickerItem) error
	// Hover is called as an item becomes the highlighted one, to show it live (a theme); Cancel when the
	// picker closes, or moves to another source, without running one, to put things back.
	Hover   func(item PickerItem)
	Cancel  func()
	Actions []PickerAction // beyond the ones every item with a Path has
	// Live sources match the query themselves, as grep does: their items are the results, not candidates.
	Live       bool
	PickerOnly bool // in the pickers, not the omnibox's merged list
	Source     string
}

func (r *reg) Picker(s PickerSource) Disposable {
	r.mu.Lock()
	defer r.mu.Unlock()
	tok := r.pickers.push(s.ID, s)
	return disposeOnce(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.pickers.remove(s.ID, tok)
	})
}

func (r *reg) Pickers() []PickerSource {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]PickerSource, 0, len(r.pickers))
	for id := range r.pickers {
		s, _ := r.pickers.top(id)
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b PickerSource) int { return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.ID, b.ID)) })
	return out
}
