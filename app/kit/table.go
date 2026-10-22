package kit

import (
	"slices"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var TStatsCard = theme.Declare("stats.card.bg", "bg.surface")

var TStatsCardLine = theme.Declare("stats.card.line", "line")

// StatsCard is a card with a kicker, a dim note after it, what's at its right, and its body.
func StatsCard(c *ui.Ctx, key any, w ui.Size, title, note string, right func(c *ui.Ctx), body func(c *ui.Ctx)) {
	th := c.Theme()
	c.Column(key, ui.Style{W: w, Pad: ui.Insets{L: 12, R: 12, T: 9, B: 10}, Gap: 8, Fill: th.C(TStatsCard), Stroke: th.C(TStatsCardLine), Border: 1,
		Radius: 8, Role: ui.RoleGroup, Label: title}, func(c *ui.Ctx) {
		c.Row("hd", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", title, KickerFont(th.C(theme.FgMuted)))
			if note != "" {
				c.Text("n", note, MonoFont(10, th.C(theme.FgGhost)))
			}
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if right != nil {
				right(c)
			}
		})
		body(c)
	})
}

// StatsCol is a table column: its title and width, right-aligned unless it's the first.
type StatsCol struct {
	Title string
	W     float32 // 0 takes what's left
}

func ColSize(w float32) ui.Size {
	if w == 0 {
		return ui.Flex(1)
	}
	return ui.Px(w)
}

// TableHead is a table's header row.
func TableHead(c *ui.Ctx, cols []StatsCol) {
	th := c.Theme()
	c.Column("head", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Row("cols", ui.Style{W: ui.Flex(1), H: ui.Px(18), Cross: ui.Center}, func(c *ui.Ctx) {
			for i, col := range cols {
				main := ui.End
				if i == 0 {
					main = ui.Start
				}
				c.Row(i, ui.Style{W: ColSize(col.W), Main: main, Pad: ui.PadXY(6, 0), Clip: true}, func(c *ui.Ctx) {
					c.Text("t", col.Title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9, Weight: typeset.SemiBold, Tracking: .1},
						Color: th.C(theme.FgGhost)})
				})
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
	})
}

// Cell is a table cell of width w: right-aligned text, or what body draws.
func Cell(c *ui.Ctx, key any, w float32, first bool, body func(c *ui.Ctx)) {
	main := ui.End
	if first {
		main = ui.Start
	}
	c.Row(key, ui.Style{W: ColSize(w), Main: main, Cross: ui.Center, Gap: 7, Pad: ui.PadXY(6, 0), Clip: true}, body)
}

func TextCell(c *ui.Ctx, key any, w float32, s string, tone render.Color) {
	Cell(c, key, w, false, func(c *ui.Ctx) { c.Text("t", s, MonoFont(11, tone)) })
}

// Swatch is a series' small square.
func Swatch(c *ui.Ctx, key any, tone render.Color) {
	c.Box(key, ui.Style{W: ui.Px(8), H: ui.Px(8), Radius: 2, Fill: tone}, nil)
}

// KV is a row of a small card: a swatch and a name, then values at the right.
func KV(c *ui.Ctx, key any, tone render.Color, name, note string, values ...string) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), H: ui.Px(20), Gap: 8, Cross: ui.Center, Role: ui.RoleText, Label: name}, func(c *ui.Ctx) {
		if tone.A > 0 {
			Swatch(c, "sw", tone)
		}
		c.Text("n", name, MonoFont(11, th.C(theme.Fg)))
		if note != "" {
			c.Text("note", note, MonoFont(10, th.C(theme.FgGhost)))
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		for i, v := range values {
			w := float32(44)
			if i == len(values)-1 {
				w = 58
			}
			c.Row(i, ui.Style{W: ui.Px(w), Main: ui.End}, func(c *ui.Ctx) {
				tone := th.C(theme.FgMuted)
				if i == len(values)-1 {
					tone = th.C(theme.Fg)
				}
				c.Text("v", v, MonoFont(11, tone))
			})
		}
	})
}

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
