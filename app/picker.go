package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

const pickerMax = 500

// picker is a picker (ui.md 02b): one source at a time, its query matched as you type, a preview of the highlighted
// result, and actions on it. ⇥ steps to the next source keeping the query; ␣' resumes the last one as it was left.
type picker struct {
	disposer
	h       plugin.Host
	open    bool
	source  string
	query   string
	sel     int
	picked  map[string]bool // multi-selected, by item id
	resumed bool
	hovered string // the source and item last given to Hover
	wait    sourceWait
	ranks   *rankings
}

// sourceWait is how long the sources shown have been loading, for their Busy row.
type sourceWait struct {
	on    bool
	since time.Time
}

// state is the sources' load: pending since they started loading, or ready.
func (w *sourceWait) state(now time.Time, loading bool) widget.LoadState {
	if !loading {
		w.on = false
		return widget.LoadState{Phase: widget.Ready}
	}
	if !w.on {
		w.on, w.since = true, now
	}
	return widget.LoadState{Phase: widget.Pending, Since: w.since}
}

// sourceLoading says a source's items are still coming.
func sourceLoading(s registry.PickerSource) bool {
	if s.Loading == nil {
		return false
	}
	busy := false
	registry.Call(func() error { busy = s.Loading(); return nil })
	return busy
}

// pickerWaitRow is the list's last row while its sources load, and "No matches" once they're done with none.
func pickerWaitRow(c *ui.Ctx, s widget.LoadState, label string, none bool, help string) {
	if s.Phase == widget.Ready && !none {
		return
	}
	c.Row("wait", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 8)}, func(c *ui.Ctx) {
		widget.Async(c, "rows", s, widget.AsyncOpts{Label: label, Empty: "No matches", IsEmpty: none, Help: help}, func(*ui.Ctx) {})
	})
}

func (p *picker) Manifest() plugin.Manifest { return plugin.Manifest{ID: "picker"} }

func (p *picker) Activate(_ context.Context, h plugin.Host) error {
	p.h, p.picked, p.ranks = h, map[string]bool{}, newRankings(h)
	p.add(onDispose(p.ranks.reset))
	p.add(h.Command(registry.Command{ID: "picker.open", Title: "Open a picker: {source}, and a {query}", Available: func(a registry.Args) bool {
		src, _ := a["source"].(string)
		return src == "" || slices.ContainsFunc(h.Pickers(), func(s registry.PickerSource) bool { return s.ID == src })
	}, Run: func(_ registry.Ctx, a registry.Args) error {
		src, _ := a["source"].(string)
		if !slices.ContainsFunc(h.Pickers(), func(s registry.PickerSource) bool { return s.ID == src }) {
			return fmt.Errorf("no picker source %q", src)
		}
		p.open, p.source, p.sel, p.picked, p.hovered = true, src, 0, map[string]bool{}, ""
		p.ranks.reset()
		p.query, _ = a["query"].(string)
		if s, ok := p.src(); ok {
			p.sel = startAt(s, p.query, h)
		}
		return nil
	}}))
	p.add(h.Command(registry.Command{ID: "picker.resume", Title: "Reopen the last picker where it was left", Run: func(registry.Ctx, registry.Args) error {
		if p.source == "" {
			return fmt.Errorf("no picker yet")
		}
		p.open, p.hovered = true, ""
		p.ranks.reset()
		return nil
	}}))
	p.add(h.View(OverlayPrefix+"picker", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "picker", Fn: p.build}
	})))
	return nil
}

func (p *picker) src() (registry.PickerSource, bool) {
	for _, s := range p.h.Pickers() {
		if s.ID == p.source {
			return s, true
		}
	}
	return registry.PickerSource{}, false
}

// actions are the source's, then the ones every place has: open, vsplit, split, tab, quickfix and attach.
func (p *picker) actions(s registry.PickerSource) []registry.PickerAction {
	out := slices.Clone(s.Actions)
	place := func(where string) func(registry.Ctx, []registry.PickerItem) error {
		return func(ctx registry.Ctx, items []registry.PickerItem) error {
			for _, it := range items {
				if it.Path == "" {
					continue
				}
				if err := p.h.Run(ctx, "editor.open", registry.Args{"path": it.Path, "line": it.Line, "place": where}); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return append(out,
		registry.PickerAction{Key: "<C-v>", Title: "vsplit", Run: place("vsplit")},
		registry.PickerAction{Key: "<C-s>", Title: "split", Run: place("split")},
		registry.PickerAction{Key: "<C-t>", Title: "tab", Run: place("tab")},
		registry.PickerAction{Key: "<C-q>", Title: "to quickfix", Run: p.toQuickfix(s)},
		registry.PickerAction{Key: "<C-a>", Title: "attach to request", Run: func(ctx registry.Ctx, items []registry.PickerItem) error {
			var refs []any
			for _, it := range items {
				if it.Path != "" {
					refs = append(refs, map[string]any{"path": it.Path, "line": it.Line, "text": it.Label})
				}
			}
			return p.h.Run(ctx, "request.attach", registry.Args{"items": refs})
		}},
	)
}

// toQuickfix sends the items with places to a new quickfix list, all of them when none are picked.
func (p *picker) toQuickfix(s registry.PickerSource) func(registry.Ctx, []registry.PickerItem) error {
	return func(ctx registry.Ctx, items []registry.PickerItem) error {
		var qf []map[string]any
		for _, it := range items {
			if it.Path != "" {
				qf = append(qf, map[string]any{"path": it.Path, "line": it.Line, "col": it.Col, "text": it.Label})
			}
		}
		return p.h.Run(ctx, "quickfix.set", registry.Args{"title": s.Title + ": " + p.query, "items": qf})
	}
}

func (p *picker) build(c *ui.Ctx) {
	if !p.open {
		return
	}
	s, ok := p.src()
	if !ok {
		p.open = false
		return
	}
	th := c.Theme()
	inputID := ui.Child(ui.Child(ui.Child(ui.Child(ui.Child(c.Next("picker"), "box"), "body"), "left"), "head"), "input")
	keys := c.Interaction(inputID).Keys
	g, _ := p.ranks.rank(s, p.query, pickerMax, len(keys) > 0)
	p.keys(c, s, g, keys)
	p.sel = min(max(p.sel, 0), max(len(g.hits)-1, 0))
	if p.open && len(g.hits) > 0 && s.Hover != nil {
		if key := s.ID + "\x00" + g.hits[p.sel].item.ID; key != p.hovered {
			p.hovered = key
			registry.Call(func() error { s.Hover(g.hits[p.sel].item); return nil })
		}
	}
	if !p.open {
		return
	}
	size := c.Size()
	at := render.Point{X: size.X / 2, Y: 108}
	if widget.Scrim(c, "picker", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(tScrimOmni), Help: "shell/pickers"}, func(c *ui.Ctx) {
		c.Column("box", ui.Style{W: ui.Px(min(1240, size.X-32)), H: ui.Px(min(720, size.Y-130)), Fill: th.C(tOmniBg), Stroke: th.C(tOmniBorder), Border: 1,
			Radius: 10, Clip: true, Trap: true, Interactive: true, Float: &ui.Float{Point: &at, Self: render.Point{X: .5}}, Role: ui.RoleDialog, Label: s.Title},
			func(c *ui.Ctx) {
				p.sourceStrip(c, s)
				c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
				c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
					c.Column("left", ui.Style{W: ui.Px(560), H: ui.Flex(1)}, func(c *ui.Ctx) {
						p.input(c, inputID, s, g)
						c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
						c.Column("list", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Insets{L: 6, R: 6, T: 6}, ScrollY: true}, func(c *ui.Ctx) {
							for i, h := range g.hits {
								if resultRow(c, i, h, i == p.sel, p.picked[h.item.ID]) {
									p.sel = i
									p.run(s, h)
								}
							}
							pickerWaitRow(c, p.wait.state(c.Now(), sourceLoading(s) || p.ranks.pending(s.ID)), s.Title+" still loading", len(g.hits) == 0 && p.query != "",
								"shell/pickers")
						})
					})
					c.Box("divider", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
					c.Column("preview", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Pad(14), Clip: true, Role: ui.RoleRegion, Label: "Preview"}, func(c *ui.Ctx) {
						if len(g.hits) > 0 && s.Preview != nil {
							var n ui.Node
							if err := registry.Call(func() error { n = s.Preview(g.hits[p.sel].item); return nil }); err == nil && n != nil {
								c.Node(n)
							}
						}
					})
				})
				c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
				p.footer(c, s, len(g.hits) > 0 && g.hits[p.sel].item.Path != "")
			})
	}) {
		p.close(true)
	}
}

// keys are the picker's own: moving, picking, the actions, and ⇥ to the next source.
func (p *picker) keys(c *ui.Ctx, s registry.PickerSource, g group, keys []render.KeyEvent) {
	acts := p.actions(s)
	for _, k := range keys {
		n := notation(k)
		switch n {
		case "<Down>", "<C-n>", "<C-j>":
			p.sel++
		case "<Up>", "<C-p>", "<C-k>":
			p.sel--
		case "<Tab>", "<S-Tab>":
			p.step(n == "<S-Tab>")
			return
		case "<C-Space>":
			if p.sel < len(g.hits) {
				id := g.hits[p.sel].item.ID
				p.picked[id] = !p.picked[id]
				p.sel++
			}
		default:
			for _, a := range acts {
				if keymap.Format(keymap.Parse(a.Key)) == n {
					p.act(a, g)
				}
			}
		}
	}
}

// selection is the picked items, or the highlighted one.
func (p *picker) selection(g group) []registry.PickerItem {
	var out []registry.PickerItem
	for _, h := range g.hits {
		if p.picked[h.item.ID] {
			out = append(out, h.item)
		}
	}
	if len(out) == 0 && p.sel < len(g.hits) {
		out = append(out, g.hits[p.sel].item)
	}
	return out
}

func (p *picker) act(a registry.PickerAction, g group) {
	items := p.selection(g)
	if a.Title == "to quickfix" && len(p.picked) == 0 {
		items = nil
		for _, h := range g.hits {
			items = append(items, h.item)
		}
	}
	p.close(false)
	if err := registry.Call(func() error { return a.Run(context.Background(), items) }); err != nil {
		p.h.Notify(core.Notice{ID: "picker.action", Severity: core.SevError, Source: "picker", Key: true, Text: a.Title + ": " + err.Error()})
	}
}

// close shuts the picker; cancelled, the source puts back what Hover showed.
func (p *picker) close(cancelled bool) {
	p.open, p.hovered = false, ""
	if s, ok := p.src(); ok && cancelled && s.Cancel != nil {
		registry.Call(func() error { s.Cancel(); return nil })
	}
}

// startAt is where a source's list starts: on its current item (the theme in use), else the top.
func startAt(s registry.PickerSource, query string, h plugin.Host) int {
	g, _ := rankSource(h, s, query, pickerMax)
	for i, x := range g.hits {
		if x.item.Current {
			return i
		}
	}
	return 0
}

func (p *picker) run(s registry.PickerSource, h hit) {
	p.close(false)
	run := s.Run
	if run == nil && h.item.Path != "" {
		run = func(ctx registry.Ctx, it registry.PickerItem) error {
			return p.h.Run(ctx, "editor.open", registry.Args{"path": it.Path, "line": it.Line})
		}
	}
	if run == nil {
		return
	}
	if err := registry.Call(func() error { return run(context.Background(), h.item) }); err != nil {
		p.h.Notify(core.Notice{ID: "picker.run", Severity: core.SevError, Source: s.Source, Key: true, Text: err.Error()})
	}
}

// step moves to the next (or previous) source in the strip, keeping the query.
func (p *picker) step(back bool) {
	var ids []string
	for _, s := range p.h.Pickers() {
		if !s.Unlisted || s.ID == p.source {
			ids = append(ids, s.ID)
		}
	}
	if s, ok := p.src(); ok && s.Cancel != nil {
		registry.Call(func() error { s.Cancel(); return nil })
	}
	p.hovered = ""
	i := slices.Index(ids, p.source)
	if back {
		i--
	} else {
		i++
	}
	p.source, p.sel, p.picked = ids[(i+len(ids))%len(ids)], 0, map[string]bool{}
}

// sourceStrip is PICK, then each source with the key that opens it; the current one lit.
func (p *picker) sourceStrip(c *ui.Ctx, cur registry.PickerSource) {
	th := c.Theme()
	label := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.Medium, Tracking: .12}, Color: th.C(theme.FgFaint)}
	c.Row("strip", ui.Style{W: ui.Flex(1), H: ui.Px(38), Pad: ui.PadXY(14, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("pick", "PICK", label)
		// More sources than fit scroll sideways, the current one brought into view when it changes.
		o := widget.StripOpts{Active: "src-" + cur.ID, Help: "shell/pickers"}
		for _, s := range p.h.Pickers() {
			if !s.Unlisted || s.ID == cur.ID {
				o.Items = append(o.Items, "src-"+s.ID)
			}
		}
		widget.Strip(c, "sources", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, o, func(c *ui.Ctx) { p.sources(c, cur, label) })
		c.Text("next", "⇥ next source", label)
	})
}

func (p *picker) sources(c *ui.Ctx, cur registry.PickerSource, label ui.TextStyle) {
	th := c.Theme()
	for _, s := range p.h.Pickers() {
		on := s.ID == cur.ID
		if s.Unlisted && !on {
			continue
		}
		text := strings.ToUpper(s.Title)
		if k := p.keyFor(s.ID); k != "" {
			text += " " + k
		}
		if widget.Clickable(c, "src-"+s.ID, ui.Style{Help: "shell/pickers", Role: ui.RoleTab, Selected: on, Label: s.Title}, func(c *ui.Ctx) {
			if on {
				widget.Chip(c, "on", text, th.C(theme.Accent), "shell/pickers")
				return
			}
			c.Row("off", ui.Style{Pad: ui.PadXY(7, 2)}, func(c *ui.Ctx) { c.Text(nil, text, label) })
		}).Clicked {
			p.source, p.sel = s.ID, 0
		}
	}
}

// keyFor is the key that opens a source's picker, as its keymap writes it, with the leader as ␣.
func (p *picker) keyFor(src string) string {
	for _, k := range p.h.Keymaps() {
		if k.Command == "picker.open" && k.Args["source"] == src && k.Args["query"] == nil {
			return strings.ReplaceAll(strings.ReplaceAll(k.Keys, "<Space>", "␣"), " ", "␣")
		}
	}
	return ""
}

func (p *picker) input(c *ui.Ctx, id ui.ID, s registry.PickerSource, g group) {
	th := c.Theme()
	in := c.Interaction(id)
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(46), Pad: ui.PadXY(14, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("prompt", "❯", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 16}, Color: th.C(theme.Accent)})
		res := widget.Input(c, "input", &p.query, widget.InputOpts{Label: "Filter " + s.Title, Width: ui.Flex(1), Help: "shell/pickers"})
		if !in.Focused && !res.Cancelled && p.open {
			c.Focus(id)
		}
		switch {
		case res.Cancelled:
			p.close(true)
		case res.Submitted && len(g.hits) > 0:
			p.run(s, g.hits[p.sel])
		case res.Changed:
			p.sel = 0
		}
		how := "fuzzy"
		if s.Live {
			how = "live"
		}
		c.Text("count", fmt.Sprintf("%d / %d · %s", len(g.hits), g.total, how),
			ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: th.C(theme.FgFaint)})
	})
}

// footer lists the keys: the place actions only when the highlighted item is a place.
func (p *picker) footer(c *ui.Ctx, s registry.PickerSource, place bool) {
	th := c.Theme()
	c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(32), Pad: ui.PadXY(14, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
		hint := func(k, what string) {
			c.Row(what, ui.Style{Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
				kbdKey(c, k)
				c.Text("w", what, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10}, Color: th.C(theme.FgFaint)})
			})
		}
		hint("↵", "open")
		acts := s.Actions
		if place {
			acts = p.actions(s)
		}
		for _, a := range acts {
			hint(strings.NewReplacer("<C-", "^", ">", "").Replace(a.Key), a.Title)
		}
		if place {
			hint("^␣", "multi-select")
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		hint("esc", "close")
	})
}
