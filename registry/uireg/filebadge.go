package uireg

import (
	"cmp"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// FileBadge is a mark a plugin puts at the right of files in the Explorer: Glyph and a count, a folder's the sum of its
// files'. Counts are each file's, by absolute path, for a session; the Explorer asks once a frame.
type FileBadge struct {
	Name   string // unique among file badges: "comments"
	Glyph  string
	Tone   string // a theme token
	Noun   string // what's counted, for the tooltip: "comment" says "3 comments"
	Help   string
	Order  int // left to right
	Counts func(session core.SessionID) map[string]int
	Source string
}

func (FileBadge) ContributionKind() string   { return "file.badge" }
func (b FileBadge) ContributionName() string { return b.Name }

// FileBadges are the Explorer's file badges, by Order.
func FileBadges(r registry.Contributions) []FileBadge {
	var out []FileBadge
	out = append(out, registry.ContributionsOf[FileBadge](r)...)
	slices.SortFunc(out, func(a, b FileBadge) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}
