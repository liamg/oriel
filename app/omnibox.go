package app

import (
	"context"
	"fmt"
	"image/color"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/fuzzy"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

const (
	omniW, omniH = 1100, 660
	omniGroupMax = 4  // results per source in the merged list
	omniScopeMax = 60 // results when narrowed to one source
)

var (
	tOmniBg      = theme.Declare("omnibox.bg", "bg.surface")
	tOmniBorder  = theme.Declare("omnibox.border", "line.strong")
	tOmniSel     = theme.DeclareOn("omnibox.row.selected", "alpha(accent, 8%)", "bg.surface")
	tOmniSelEdge = theme.Declare("omnibox.row.selected.edge", "accent")
	tOmniMatch   = theme.Declare("omnibox.match", "accent")
	tOmniHead    = theme.Declare("omnibox.group", "fg.faint")
)

// omnibox is ⌘K: every picker source merged into one ranked list, with a preview of the selection (ui.md 02).
// A source's prefix narrows it to that source; ⇥ steps through them.
type omnibox struct {
	disposer
	h     ext.Host
	open  bool
	query string
	scope string // a source's id, or "" for all
	sel   int
}

func (o *omnibox) Manifest() ext.Manifest { return ext.Manifest{ID: "omnibox"} }

func (o *omnibox) Activate(_ context.Context, h ext.Host) error {
	o.h = h
	o.add(h.Command(registry.Command{ID: "omnibox.open", Title: "Search everything", Run: func(_ registry.Ctx, a registry.Args) error {
		o.open, o.sel = true, 0
		o.query, _ = a["query"].(string)
		o.scope, _ = a["scope"].(string)
		return nil
	}}))
	o.add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-k>", Command: "omnibox.open", Desc: "Search everything"}))
	o.add(h.View(OverlayPrefix+"omnibox", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "omnibox", Fn: o.build}
	})))
	return nil
}

// hit is a ranked result.
type hit struct {
	src   registry.PickerSource
	item  registry.PickerItem
	score int
	pos   []int
}

// group is a source's results, best first, and how many it had.
type group struct {
	src   registry.PickerSource
	hits  []hit
	total int
}

// results ranks every source's items against the query: the fuzzy score plus the item's boost, best first, each
// source in its order. A source's prefix at the start of the query narrows to it.
func (o *omnibox) results() []group {
	q, scope := o.query, o.scope
	srcs := o.h.Pickers()
	for _, s := range srcs {
		if s.Prefix != "" && strings.HasPrefix(q, s.Prefix) {
			q, scope = strings.TrimPrefix(q, s.Prefix), s.ID
			break
		}
	}
	limit := omniGroupMax
	if scope != "" {
		limit = omniScopeMax
	}
	var out []group
	for _, s := range srcs {
		if scope != "" && s.ID != scope || scope == "" && s.PickerOnly {
			continue
		}
		if g, ok := rankSource(o.h, s, q, limit); ok {
			out = append(out, g)
		}
	}
	return out
}

// rankSource is a source's items for query, best first, at most limit of them. A live source's items are its
// results already, in its order.
func rankSource(h ext.Host, s registry.PickerSource, q string, limit int) (group, bool) {
	if s.Items == nil {
		return group{}, false
	}
	var items []registry.PickerItem
	if err := registry.Call(func() error { items = s.Items(context.Background(), q); return nil }); err != nil {
		h.TurnOff("picker", s.ID, err)
		return group{}, false
	}
	terms := fuzzy.Parse(q)
	var hits []hit
	for _, it := range items {
		if s.Live {
			hits = append(hits, hit{s, it, it.Boost, nil})
			continue
		}
		text := it.Label
		if it.Detail != "" {
			text += "  " + it.Detail // matched too, as fzf matches the whole line; only the label's matches light
		}
		score, pos, ok := fuzzy.MatchQuery(terms, text)
		if !ok {
			continue
		}
		hits = append(hits, hit{s, it, score + it.Boost, pos})
	}
	if !s.Live {
		slices.SortStableFunc(hits, func(a, b hit) int { return b.score - a.score })
	}
	if len(hits) == 0 {
		return group{src: s}, false
	}
	return group{src: s, hits: hits[:min(len(hits), limit)], total: len(hits)}, true
}

func (o *omnibox) build(c *ui.Ctx) {
	if !o.open {
		return
	}
	th := c.Theme()
	groups := o.results()
	var flat []hit
	for _, g := range groups {
		flat = append(flat, g.hits...)
	}
	inputID := ui.Child(ui.Child(ui.Child(c.Next("omni"), "box"), "head"), "input")
	in := c.Interaction(inputID)
	for _, k := range in.Keys {
		switch {
		case k.Key == "Down" || k.Key == "n" && k.Mods == render.ModCtrl:
			o.sel++
		case k.Key == "Up" || k.Key == "p" && k.Mods == render.ModCtrl:
			o.sel--
		case k.Key == "Tab":
			o.cycleScope(k.Mods&render.ModShift != 0)
		}
	}
	o.sel = min(max(o.sel, 0), max(len(flat)-1, 0))
	size := c.Size()
	centre := render.Point{X: size.X / 2, Y: 92}
	c.Box("omni", ui.Style{W: ui.Px(size.X), H: ui.Px(size.Y), Z: 990, Fill: th.C(tScrimOmni), Interactive: true, Float: &ui.Float{Point: &render.Point{}},
		Help: "shell/omnibox"}, func(c *ui.Ctx) {
		c.Column("box", ui.Style{W: ui.Px(min(omniW, size.X-32)), H: ui.Px(min(omniH, size.Y-110)), Fill: th.C(tOmniBg), Stroke: th.C(tOmniBorder),
			Border: 1, Radius: 10, Clip: true, Trap: true, Z: 991, Float: &ui.Float{Point: &centre, Self: render.Point{X: .5}}, Role: ui.RoleDialog,
			Label: "Search everything"}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(58), Pad: ui.PadXY(18, 0), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("cmd", "⌘", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 18}, Color: th.C(theme.Accent)})
				res := widget.Input(c, "input", &o.query, widget.InputOpts{Label: "Search", Placeholder: "Search sessions, commands, files, symbols…",
					Width: ui.Flex(1), Help: "shell/omnibox"})
				if !in.Focused && !res.Cancelled {
					c.Focus(inputID)
				}
				switch {
				case res.Cancelled:
					o.open = false
				case res.Submitted && len(flat) > 0:
					o.run(flat[o.sel])
				case res.Changed:
					o.sel = 0
				}
				o.chips(c)
			})
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				c.Column("list", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Insets{L: 8, R: 8, T: 12}, ScrollY: true}, func(c *ui.Ctx) {
					o.list(c, groups)
				})
				c.Box("divider", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
				c.Column("preview", ui.Style{W: ui.Px(400), H: ui.Flex(1), Pad: ui.Pad(18), Clip: true, Role: ui.RoleRegion, Label: "Preview"}, func(c *ui.Ctx) {
					if len(flat) > 0 && flat[o.sel].src.Preview != nil {
						h := flat[o.sel]
						var n ui.Node
						if err := registry.Call(func() error { n = h.src.Preview(h.item); return nil }); err == nil && n != nil {
							c.Node(n)
						}
					}
				})
			})
			c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(34), Pad: ui.PadXY(18, 0), Gap: 16, Cross: ui.Center}, func(c *ui.Ctx) {
				hint := func(k, what string) {
					c.Row(what, ui.Style{Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
						kbdKey(c, k)
						c.Text("w", what, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10}, Color: th.C(theme.FgFaint)})
					})
				}
				hint("↵", "open")
				hint("⇥", "scope")
				hint("esc", "close")
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("how", "fuzzy · ranked by match, then recency and what needs you",
					ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10}, Color: th.C(theme.FgFaint)})
			})
		})
	})
}

var tScrimOmni = theme.Declare("omnibox.scrim", "alpha(#030508, 60%)")

func kbdKey(c *ui.Ctx, k string) {
	th := c.Theme()
	c.Row("kbd", ui.Style{Pad: ui.PadXY(5, 1), Fill: th.C(theme.BgRaised), Stroke: th.C(theme.LineStrong), Border: 1, Radius: 4}, func(c *ui.Ctx) {
		c.Text(nil, k, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10}, Color: th.C(theme.FgMuted)})
	})
}

// chips are the scopes: all, then each source, with its prefix. The current one is lit.
func (o *omnibox) chips(c *ui.Ctx) {
	th := c.Theme()
	chip := func(id, label string) {
		key := c.Next(fmt.Sprint("chip-", id))
		if c.Interaction(key).Clicked {
			o.scope, o.sel = id, 0
		}
		tone := th.C(theme.FgMuted)
		if o.scope == id {
			tone = th.C(theme.Accent)
		}
		c.Box(fmt.Sprint("chip-", id), ui.Style{Interactive: true, Cursor: render.CursorPointer, Help: "shell/omnibox", Role: ui.RoleButton, Label: label,
			Selected: o.scope == id}, func(c *ui.Ctx) {
			widget.Chip(c, "c", label, tone, "shell/omnibox")
		})
	}
	chip("", "All")
	for _, s := range o.h.Pickers() {
		if s.PickerOnly {
			continue
		}
		label := s.Title
		if s.Prefix != "" {
			label += " " + s.Prefix
		}
		chip(s.ID, label)
	}
}

func (o *omnibox) cycleScope(back bool) {
	ids := []string{""}
	for _, s := range o.h.Pickers() {
		if !s.PickerOnly {
			ids = append(ids, s.ID)
		}
	}
	i := slices.Index(ids, o.scope)
	if back {
		i--
	} else {
		i++
	}
	o.scope, o.sel = ids[(i+len(ids))%len(ids)], 0
}

func (o *omnibox) run(h hit) {
	o.open = false
	if h.src.Run == nil {
		return
	}
	if err := registry.Call(func() error { return h.src.Run(context.Background(), h.item) }); err != nil {
		o.h.Notify(core.Notice{ID: "omnibox.run", Severity: core.SevError, Source: h.src.Source, Key: true, Text: err.Error()})
	}
}

// list is the results, grouped by source under its title and count.
func (o *omnibox) list(c *ui.Ctx, groups []group) {
	th := c.Theme()
	if len(groups) == 0 {
		c.Text("none", "No matches", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13}, Color: th.C(theme.FgFaint)})
		return
	}
	n := 0
	for _, g := range groups {
		c.Row("head-"+g.src.ID, ui.Style{W: ui.Flex(1), H: ui.Px(26), Pad: ui.PadXY(10, 0), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", strings.ToUpper(g.src.Title), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.Medium,
				Tracking: .14}, Color: th.C(tOmniHead)})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if g.total > len(g.hits) {
				c.Text("n", fmt.Sprintf("%d of %d", len(g.hits), g.total), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5},
					Color: th.C(tOmniHead)})
			}
		})
		for _, h := range g.hits {
			if resultRow(c, n, h, n == o.sel, false) {
				o.sel = n
				o.run(h)
			}
			n++
		}
	}
}

// resultRow is one result: its icon, label with the matched characters lit, detail, note, badge and key. on is
// the highlighted row; picked a row multi-selected. It reports a click.
func resultRow(c *ui.Ctx, i int, h hit, on, picked bool) bool {
	th := c.Theme()
	id := c.Next(fmt.Sprint("r", i))
	clicked := c.Interaction(id).Clicked
	fill, edge := color.NRGBA{}, color.NRGBA{}
	if picked {
		edge = th.C(theme.Warn)
	}
	if on {
		fill, edge = th.C(tOmniSel), th.C(tOmniSelEdge)
	}
	it := h.item
	c.Row(fmt.Sprint("r", i), ui.Style{W: ui.Flex(1), H: ui.Px(32), Pad: ui.Insets{R: 10}, Gap: 10, Cross: ui.Center, Fill: fill, Radius: 6,
		Interactive: true, Cursor: render.CursorPointer, Help: "shell/omnibox", Role: ui.RoleListItem, Selected: on, Label: it.Label}, func(c *ui.Ctx) {
		c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Px(20), Fill: edge}, nil)
		icon := tokenColour(th, it.IconStyle, th.C(theme.FgFaint))
		c.Row("icon", ui.Style{W: ui.Px(16), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
			if it.Icon == "" {
				c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: icon}, nil)
			} else {
				c.Text("g", it.Icon, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: icon})
			}
		})
		matchedLabel(c, it.Label, h.pos, on)
		if it.Detail != "" {
			c.Text("detail", it.Detail, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: th.C(theme.FgFaint)})
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if it.Note != "" {
			c.Text("note", it.Note, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10}, Color: th.C(theme.FgGhost)})
		}
		if it.Badge != "" {
			widget.Chip(c, "badge", it.Badge, tokenColour(th, it.BadgeStyle, th.C(theme.FgMuted)), "shell/omnibox")
		}
		if it.Key != "" {
			kbdKey(c, it.Key)
		}
	})
	return clicked
}

// matchedLabel draws a label with the matched characters in the match colour.
func matchedLabel(c *ui.Ctx, label string, pos []int, on bool) {
	th := c.Theme()
	base := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13}, Color: th.C(theme.Fg)}
	if on {
		base.Font.Weight = typeset.SemiBold
	}
	hl := base
	hl.Color = th.C(tOmniMatch)
	hl.Font.Weight = typeset.SemiBold
	in := map[int]bool{}
	for _, p := range pos {
		in[p] = true
	}
	c.Row("label", ui.Style{Cross: ui.Center}, func(c *ui.Ctx) {
		rs := []rune(label)
		for i := 0; i < len(rs); {
			j := i
			for j < len(rs) && in[j] == in[i] {
				j++
			}
			st := base
			if in[i] {
				st = hl
			}
			c.Text(i, string(rs[i:j]), st)
			i = j
		}
	})
}
