package kit

import (
	"slices"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
)

// RepoTones are repos' colours, each its own: a repo of the session in front keeps its project colour there (the
// sidebar's), and the others take the palette's colours it doesn't use, in order.
func RepoTones(th *theme.Resolved, st core.State, names []string) []render.Color {
	out := make([]render.Color, len(names))
	used := map[string]bool{}
	got := make([]string, len(names))
	if se, ok := st.Session(st.Focus); ok {
		s := se.Snapshot()
		for i, n := range names {
			if HasProject(s, n) {
				got[i] = s.ProjectColour(n)
				used[got[i]] = true
			}
		}
	}
	next := 0
	for i := range names {
		for got[i] == "" && next < len(core.ProjectColours) {
			if c := core.ProjectColours[next]; !used[c] {
				got[i], used[c] = c, true
			}
			next++
		}
		out[i] = TokenColour(th, got[i], PaletteTone(th, i))
	}
	return out
}

// RepoTone is a repo's colour, the same wherever the page draws it: among the session in front's repos and then
// every other repo the app knows, by name.
func RepoTone(th *theme.Resolved, st core.State, name string, _ int) render.Color {
	var names, rest []string
	if se, ok := st.Session(st.Focus); ok {
		for _, p := range se.Snapshot().Projects {
			names = append(names, p.Name)
		}
	}
	for _, se := range st.Sessions {
		for _, p := range se.Projects {
			if !slices.Contains(names, p.Name) && !slices.Contains(rest, p.Name) {
				rest = append(rest, p.Name)
			}
		}
	}
	slices.Sort(rest)
	if !slices.Contains(rest, name) && !slices.Contains(names, name) {
		rest = append(rest, name)
	}
	all := append(names, rest...)
	return RepoTones(th, st, all)[slices.Index(all, name)]
}

func HasProject(se core.SessionState, name string) bool {
	for _, p := range se.Projects {
		if p.Name == name {
			return true
		}
	}
	return false
}

func PaletteTone(th *theme.Resolved, rank int) render.Color {
	return TokenColour(th, core.ProjectColours[rank%len(core.ProjectColours)], th.C(theme.Accent))
}
