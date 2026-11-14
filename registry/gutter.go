package registry

import (
	"cmp"
	"slices"
	"strings"
)

// GutterAction is what a click on an editor's line numbers does in the files it applies to: runs Command with Args
// and the file and the 1-based line clicked ({file, line}). The breakpoint toggle is one.
type GutterAction struct {
	Name    string
	Command string
	Args    Args
	When    func(path string) bool // nil: every file
	Order   int                    // the lowest that applies wins
	Source  string
}

func (GutterAction) ContributionKind() string   { return "editor.gutter" }
func (a GutterAction) ContributionName() string { return a.Name }

// GutterActionFor is the gutter action for a file, if any applies.
func GutterActionFor(r Contributions, path string) (GutterAction, bool) {
	var out []GutterAction
	for _, a := range ContributionsOf[GutterAction](r) {
		ok := a.When == nil
		if !ok {
			Call(func() error { ok = a.When(path); return nil })
		}
		if ok {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return GutterAction{}, false
	}
	slices.SortFunc(out, func(a, b GutterAction) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out[0], true
}
