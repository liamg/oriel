package shell

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/script/api"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/kit/fuzzy"
	"github.com/liamg/oriel/app/shell/help"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tHelpMarker   = theme.Declare("help.marker", "accent")
	tHelpMarkerBg = theme.Declare("help.marker.bg", "bg.raised")
	tHelpLink     = theme.Declare("help.link", "accent")
	tHelpLabel    = theme.Declare("help.label", "fg.faint")
)

// helpView is help everywhere: pages from every contributor, opened from a right-click, F1, help mode
// (⌘?), :help and the ␣h picker.
type helpView struct {
	kit.Disposer
	configDir string
	h         plugin.Host
	ix        *help.Index
	gen       int
	open      bool
	cur       string   // the help id shown
	back      []string // the pages before it, for ⌃o
	fwd       []string // and after, for ⌃i
	mode      bool     // help mode: a ? on everything with help
	into      bool     // help mode asked for focus
}

func (v *helpView) Manifest() plugin.Manifest { return plugin.Manifest{ID: "help"} }

func (v *helpView) Activate(_ context.Context, h plugin.Host) error {
	v.h = h
	v.Add(h.HelpPages(registry.HelpSource{FS: help.Builtin(), Source: "builtin"}))
	if dir := filepath.Join(v.configDir, "help"); dirExists(dir) {
		v.Add(h.HelpPages(registry.HelpSource{FS: os.DirFS(dir), Source: "config"}))
	}
	for _, c := range []registry.Command{
		{ID: "help.open", Title: "Open a help page: {id}, page#anchor", Run: func(_ registry.Ctx, a registry.Args) error {
			return v.show(a.String("id"))
		}},
		{ID: "help", Title: "Help on a topic", Run: func(_ registry.Ctx, a registry.Args) error {
			topic := strings.TrimSpace(a.String("args") + a.String("topic"))
			if topic == "" {
				return v.show("shell/help")
			}
			return v.show(v.find(topic))
		}},
		{ID: "help.focused", Title: "Help on what has focus", Run: func(registry.Ctx, registry.Args) error {
			u := h.UI()
			if u == nil {
				return nil
			}
			id := u.Help(u.Focused())
			if id == "" {
				id = "shell/help"
			}
			return v.show(id)
		}},
		{ID: "help.mode", Title: "Help mode: a ? on everything with help", Run: func(registry.Ctx, registry.Args) error {
			v.mode, v.into = !v.mode, false
			return nil
		}},
		{ID: "help.back", Title: "Back to the help page before", Run: func(registry.Ctx, registry.Args) error {
			if n := len(v.back); n > 0 {
				v.fwd = append(v.fwd, v.cur)
				v.cur, v.back = v.back[n-1], v.back[:n-1]
			}
			return nil
		}},
		{ID: "help.forward", Title: "Forward to the help page after", Run: func(registry.Ctx, registry.Args) error {
			if n := len(v.fwd); n > 0 {
				v.back = append(v.back, v.cur)
				v.cur, v.fwd = v.fwd[n-1], v.fwd[:n-1]
			}
			return nil
		}},
	} {
		v.Add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Mode: "global", Keys: "<F1>", Command: "help.focused", Desc: "Help on what has focus"},
		{Mode: "global", Keys: "<D-?>", Command: "help.mode", Desc: "Help mode"},
		{Mode: "help", Keys: "<C-o>", Command: "help.back", Desc: "Back"},
		{Mode: "help", Keys: "<C-i>", Command: "help.forward", Desc: "Forward"},
		{Mode: "help", Keys: "<X1Mouse>", Command: "help.back", Desc: "Back"},
		{Mode: "help", Keys: "<X2Mouse>", Command: "help.forward", Desc: "Forward"},
		{Mode: "n", Keys: "<Leader>h", Command: "picker.open", Args: registry.Args{"source": "help"}, Desc: "Help"},
	} {
		v.Add(h.Keymap(k))
	}
	// K is help on what has focus, in the widgets' own modes: not in an input, or the editor, where K is vim's.
	for _, mode := range []string{"button", "list", "tree", "tabs", "menu", "toast", "datepicker"} {
		v.Add(h.Keymap(registry.Keymap{Mode: mode, Keys: "K", Command: "help.focused", Desc: "Help on this"}))
	}
	v.Add(h.Picker(v.source()))
	for _, t := range []registry.AgentTool{
		{Name: "help_search", Params: map[string]string{"query": "string"}, ReadOnly: true, Reads: "help", Run: v.toolSearch,
			Desc: "Search Oriel's help pages, for how to do something in Oriel: the best sections first, each with its id, title and the line that matched. Read one with help_read."},
		{Name: "help_read", Params: map[string]string{"id": "string"}, ReadOnly: true, Reads: "help", Run: v.toolRead,
			Desc: "Read a help section as markdown, by its id from help_search (page or page#anchor), with the keys and settings it lists as they are now."},
		{Name: "open_help", Params: map[string]string{"id": "string"}, Run: v.toolOpen,
			Desc: "Open a help section in Oriel's help viewer, for the user to read, by its id from help_search."},
	} {
		t.For, t.Source = []string{kit.AssistantAgent}, "builtin:help"
		v.Add(h.Contribute(t))
	}
	v.Add(h.View(kit.OverlayPrefix+"help", uireg.ViewFunc(func(vc uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "help", Fn: func(c *ui.Ctx) { v.build(c, vc) }}
	})))
	return nil
}

func dirExists(dir string) bool {
	st, err := os.Stat(dir)
	return err == nil && st.IsDir()
}

// index is the pages, read again when a contributor adds or drops some.
func (v *helpView) index() *help.Index {
	srcs, gen := v.h.HelpSources()
	if v.ix == nil || gen != v.gen {
		var all []help.Source
		for _, s := range srcs {
			all = append(all, help.Source{Name: s.Source, FS: s.FS})
		}
		ix, errs := help.Load(all)
		for _, err := range errs {
			v.h.Log().Warn("help page", "err", err)
		}
		v.ix, v.gen = ix, gen
	}
	return v.ix
}

// find is the best topic for what :help was given: an id, an anchor, or words of a title.
func (v *helpView) find(topic string) string {
	ix := v.index()
	if _, _, ok := ix.For(topic); ok {
		return topic
	}
	best, score := "", -1
	terms := fuzzy.Parse(topic)
	for _, hit := range ix.Topics() {
		if strings.HasSuffix(hit.ID, "#"+topic) {
			return hit.ID
		}
		if s, _, ok := fuzzy.MatchQuery(terms, hit.Title+" "+hit.ID); ok && s > score {
			best, score = hit.ID, s
		}
	}
	return best
}

// show opens a help id, the page before it going on the back stack.
func (v *helpView) show(id string) error {
	if id == "" {
		return fmt.Errorf("no help on that")
	}
	if _, _, ok := v.index().For(id); !ok {
		return fmt.Errorf("no help page %q", id)
	}
	if v.open && v.cur != "" && v.cur != id {
		v.back, v.fwd = append(v.back, v.cur), nil
	}
	v.open, v.cur, v.mode = true, id, false
	return nil
}

// source is the ␣h picker: every page and section.
func (v *helpView) source() uireg.PickerSource {
	return uireg.PickerSource{ID: "help", Title: "Help", Order: 70, PickerOnly: true,
		Items: func(registry.Ctx, string) []uireg.PickerItem {
			var out []uireg.PickerItem
			for _, hit := range v.index().Topics() {
				out = append(out, uireg.PickerItem{ID: hit.ID, Label: hit.Title, Detail: hit.ID, Icon: "?", Data: hit})
			}
			return out
		},
		Preview: func(it uireg.PickerItem) ui.Node {
			return &ui.Func{Key: "help", Fn: func(c *ui.Ctx) { v.body(c, it.ID, 480) }}
		},
		Run: func(_ registry.Ctx, it uireg.PickerItem) error { return v.show(it.ID) },
	}
}

func (v *helpView) title(id string) string {
	p, anchor, ok := v.index().For(id)
	if !ok {
		return id
	}
	if head, _, ok := p.Section(anchor); ok && head.Text != "" {
		return head.Text
	}
	return p.Title
}

func (v *helpView) build(c *ui.Ctx, vc uireg.ViewCtx) {
	if u := v.h.UI(); u != nil {
		if id, ok := u.HelpRequest(); ok {
			v.show(id)
		}
	}
	if v.mode {
		v.markers(c)
	}
	if v.open {
		v.page(c)
	}
}

// markers are help mode's: a ? on everything with help, which opens its page.
func (v *helpView) markers(c *ui.Ctx) {
	th := c.Theme()
	u := v.h.UI()
	if u == nil {
		return
	}
	keys := c.Next("helpmode")
	for _, k := range c.Interaction(keys).Keys {
		if k.Key == "Esc" {
			v.mode = false
			return
		}
	}
	c.Box("helpmode", ui.Style{W: ui.Px(0), H: ui.Px(0), Focusable: true, KeyMode: "helpmode", Help: "shell/help"}, nil)
	if !v.into {
		v.into = true
		c.RequestFocus(keys)
	}
	seen := map[string]bool{}
	for i, n := range u.HelpNodes() {
		if seen[n.Help] || n.Rect.Dx() < 8 {
			continue
		}
		seen[n.Help] = true
		at := render.Point{X: n.Rect.Max.X - 6, Y: n.Rect.Min.Y - 4}
		if widget.Clickable(c, fmt.Sprint("m", i), ui.Style{W: ui.Px(14), H: ui.Px(14), Radius: 7, Fill: th.C(tHelpMarkerBg), Stroke: th.C(tHelpMarker),
			Border: 1, Main: ui.Center, Cross: ui.Center, Z: ui.LayerOverlay, Float: &ui.Float{Point: &at}, Tooltip: v.title(n.Help), Help: "shell/help",
			Label: "Help: " + v.title(n.Help)}, func(c *ui.Ctx) {
			c.Text("q", "?", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9, Weight: typeset.Bold}, Color: th.C(tHelpMarker)})
		}).Clicked {
			v.show(n.Help)
		}
	}
}

// page is the open help page (28-help): its path, the section asked for, and the related pages beside it.
func (v *helpView) page(c *ui.Ctx) {
	th := c.Theme()
	p, anchor, ok := v.index().For(v.cur)
	if !ok {
		v.open = false
		return
	}
	size := c.Size()
	at := render.Point{X: size.X / 2, Y: 118}
	boxID := ui.Child(c.Next("helppage"), "box")
	for _, k := range c.Interaction(boxID).Keys {
		if k.Key == "Esc" {
			v.open = false
			return
		}
	}
	label := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.Medium, Tracking: .14}, Color: th.C(tHelpLabel)}
	if widget.Scrim(c, "helppage", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(kit.TScrimOmni), Help: "shell/help"}, func(c *ui.Ctx) {
		c.Column("box", ui.Style{W: ui.Px(min(880, size.X-32)), H: ui.Px(min(800, size.Y-140)), Fill: th.C(kit.TOmniBg), Stroke: th.C(kit.TOmniBorder), Border: 1,
			Radius: 10, Clip: true, Trap: true, Focusable: true, KeyMode: "help", Float: &ui.Float{Point: &at, Self: render.Point{X: .5}},
			Role: ui.RoleDialog, Label: "Help: " + v.title(v.cur), Help: "shell/help"}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(40), Pad: ui.PadXY(16, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("help", "HELP", ui.TextStyle{Font: label.Font, Color: th.C(theme.Accent)})
				crumbs := strings.Split(p.ID, "/")
				if anchor != "" {
					crumbs = append(crumbs, anchor)
				}
				for i, s := range crumbs {
					if i > 0 {
						c.Text(fmt.Sprint("sep", i), "›", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgGhost)})
					}
					c.Text(i, s, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgMuted)})
				}
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				widget.Chip(c, "file", path.Join(p.Source, p.File), th.C(theme.FgFaint), "shell/help")
				kit.KbdKey(c, "esc")
			})
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				c.Column("main", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Pad(28), Gap: 12, ScrollY: true}, func(c *ui.Ctx) {
					v.body(c, v.cur, min(880, size.X-32)-260-56)
				})
				c.Box("divider", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
				c.Column("side", ui.Style{W: ui.Px(250), H: ui.Flex(1), Pad: ui.Pad(16), Gap: 8, Clip: true}, func(c *ui.Ctx) {
					c.Text("related", "RELATED", label)
					for i, r := range p.Related {
						if widget.Clickable(c, fmt.Sprint("rel", i), ui.Style{Dir: ui.Row, Gap: 6, Cross: ui.Center, Help: "shell/help", Label: v.title(r)},
							func(c *ui.Ctx) {
								c.Text("t", v.title(r), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5}, Color: th.C(tHelpLink)})
								c.Text("id", r, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5}, Color: th.C(theme.FgGhost)})
							}).Clicked {
							v.show(r)
						}
					}
					c.Box("gap1", ui.Style{H: ui.Px(10)}, nil)
					c.Text("this", "THIS PAGE", label)
					small := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10}, Color: th.C(theme.FgFaint)}
					c.Text("id", "help: "+v.cur, small)
					c.Text("from", "from: "+p.Source, small)
					if len(p.Covers) > 0 {
						c.Text("covers", "covers: "+strings.Join(p.Covers, ", "), small)
					}
					c.Box("gap2", ui.Style{H: ui.Flex(1)}, nil)
					c.Text("nav", kit.KeyLabel("<C-o>")+" back · "+kit.KeyLabel("<C-i>")+" forward · esc close", small)
				})
			})
		})
	}) {
		v.open = false
	}
}

// body is a help id's section: its title, then its blocks, the generated ones filled from the registries.
func (v *helpView) body(c *ui.Ctx, id string, width float32) {
	th := c.Theme()
	p, anchor, ok := v.index().For(id)
	if !ok {
		return
	}
	head, blocks, _ := p.Section(anchor)
	text := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13.5}, Color: th.C(theme.Fg)}
	label := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.Medium, Tracking: .14}, Color: th.C(tHelpLabel)}
	c.Column("body", ui.Style{W: ui.Flex(1), Gap: 12, Selectable: true}, func(c *ui.Ctx) {
		c.Text("title", head.Text, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 20, Weight: typeset.Medium}, Color: th.C(theme.Fg)})
		for i, b := range blocks {
			switch b.Kind {
			case "heading":
				c.Text(i, strings.ToUpper(b.Text), label)
			case "para":
				widget.Wrap(c, i, plainText(b.Text), width, text)
			case "list":
				c.Column(i, ui.Style{Gap: 6}, func(c *ui.Ctx) {
					for j, it := range b.Items {
						c.Row(j, ui.Style{Gap: 8}, func(c *ui.Ctx) {
							c.Text("dot", "·", text)
							widget.Wrap(c, "t", plainText(it), width-20, text)
						})
					}
				})
			case "code":
				c.Column(i, ui.Style{W: ui.Flex(1), Pad: ui.Pad(10), Fill: th.C(theme.BgBase), Radius: 6}, func(c *ui.Ctx) {
					for j, l := range strings.Split(b.Text, "\n") {
						c.Text(j, kit.ExpandTabs(l), ui.TextStyle{Font: kit.CodeFont, Color: th.C(theme.Fg)})
					}
				})
			case "keys":
				v.keys(c, i, b.Text)
			case "options":
				v.options(c, i, b.Text)
			case "live":
				if fn, ok := v.h.HelpLiveByName(b.Text); ok {
					c.Column(i, ui.Style{W: ui.Flex(1), Pad: ui.Pad(14), Stroke: th.C(theme.Warn), Border: 1, Radius: 6, Gap: 6}, func(c *ui.Ctx) {
						c.Text("now", "RIGHT NOW", label)
						fn(c)
					})
				}
			case "api":
				v.api(c, i, b.Text)
			}
		}
	})
}

// plainText is a paragraph's markdown as plain text: emphasis and code marks dropped.
func plainText(s string) string {
	return strings.NewReplacer("**", "", "`", "").Replace(s)
}

// keys is {{keys "glob"}}: the keymaps whose command matches glob, from the registry.
func (v *helpView) keys(c *ui.Ctx, key any, glob string) {
	th := c.Theme()
	var rows []registry.Keymap
	for _, k := range v.h.Keymaps() {
		if ok, _ := path.Match(glob, k.Command); ok && k.Command != "" {
			rows = append(rows, k)
		}
	}
	if len(rows) == 0 {
		return
	}
	c.Column(key, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Text("label", "KEYS", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.Medium, Tracking: .14}, Color: th.C(tHelpLabel)})
		for i, k := range rows {
			c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(30), Gap: 14, Cross: ui.Center, Stroke: th.C(theme.Line)}, func(c *ui.Ctx) {
				c.Row("k", ui.Style{W: ui.Px(140)}, func(c *ui.Ctx) { kit.KbdKey(c, kit.KeyLabel(k.Keys)) })
				desc := k.Desc
				if desc == "" {
					desc = k.Command
				}
				c.Text("d", desc, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13}, Color: th.C(theme.Fg)})
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("m", k.Mode, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10}, Color: th.C(theme.FgGhost)})
			})
		}
	})
}

// options is {{options "prefix"}}: the options under prefix, with their values now.
func (v *helpView) options(c *ui.Ctx, key any, prefix string) {
	th := c.Theme()
	var specs []registry.OptionSpec
	for _, s := range v.h.Options().Specs() {
		if s.Name == prefix || strings.HasPrefix(s.Name, prefix+".") {
			specs = append(specs, s)
		}
	}
	if len(specs) == 0 {
		return
	}
	c.Column(key, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Text("label", "SETTINGS", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.Medium, Tracking: .14}, Color: th.C(tHelpLabel)})
		for i, s := range specs {
			val, _, _ := v.h.Options().Get(s.Name, registry.At{})
			c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(30), Gap: 14, Cross: ui.Center, Stroke: th.C(theme.Line)}, func(c *ui.Ctx) {
				c.Text("n", s.Name, ui.TextStyle{Font: kit.CodeFont, Color: th.C(theme.Fg)})
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("v", fmt.Sprint(val), ui.TextStyle{Font: kit.CodeFont, Color: th.C(theme.FgMuted)})
			})
		}
	})
}

// api is a namespace's reference from the generated API (docs/api.md), each call and what it does.
func (v *helpView) api(c *ui.Ctx, key any, ns string) {
	th := c.Theme()
	name := strings.TrimPrefix(strings.TrimPrefix(ns, "oriel"), ".")
	fns := api.Reference[name]
	c.Column(key, ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
		if len(fns) == 0 {
			c.Text("none", "No functions in oriel."+name+".", ui.TextStyle{Font: kit.CodeFont, Color: th.C(theme.FgFaint)})
			return
		}
		for i, f := range fns {
			c.Column(i, ui.Style{W: ui.Flex(1), Gap: 2}, func(c *ui.Ctx) {
				c.Text("call", f.Call, ui.TextStyle{Font: kit.CodeFont, Color: th.C(theme.Accent)})
				c.Text("doc", f.Doc, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13}, Color: th.C(theme.FgMuted)})
			})
		}
	})
}

const helpSearchHits = 8

func (v *helpView) toolSearch(ctx registry.Ctx, p map[string]any) (any, error) {
	q := strings.TrimSpace(kit.ParamString(p, "query"))
	if q == "" {
		return nil, errors.New("search for what?")
	}
	var hits []help.Result
	if err := kit.OnLoop(ctx, v.h, func() { hits = v.index().Search(q, helpSearchHits, v.expand) }); err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("no help page mentions %q: try other words", q)
	}
	return hits, nil
}

type helpSection struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

func (v *helpView) toolRead(ctx registry.Ctx, p map[string]any) (any, error) {
	id := kit.ParamString(p, "id")
	var out helpSection
	ok := false
	if err := kit.OnLoop(ctx, v.h, func() { out.Title, out.Text, ok = v.index().SectionText(id, v.expand) }); err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no help section %q: find one with help_search", id)
	}
	out.ID = id
	return out, nil
}

func (v *helpView) toolOpen(ctx registry.Ctx, p map[string]any) (any, error) {
	id := kit.ParamString(p, "id")
	var err error
	if e := kit.OnLoop(ctx, v.h, func() { err = v.show(id) }); e != nil {
		return nil, e
	}
	if err != nil {
		return nil, err
	}
	return registry.Acted{Result: "opened", Line: "opened help: " + v.title(id)}, nil
}

// expand is a directive as text, as the page draws it now: the keys and settings it lists, an API's calls.
func (v *helpView) expand(b help.Block) string {
	var lines []string
	switch b.Kind {
	case "keys":
		for _, k := range v.h.Keymaps() {
			if ok, _ := path.Match(b.Text, k.Command); ok && k.Command != "" {
				lines = append(lines, fmt.Sprintf("- %s (%s, %s mode): %s", kit.KeyLabel(k.Keys), k.Keys, k.Mode, cmp.Or(k.Desc, k.Command)))
			}
		}
		if len(lines) > 0 {
			return "Keys:\n" + strings.Join(lines, "\n")
		}
	case "options":
		for _, s := range v.h.Options().Specs() {
			if s.Name == b.Text || strings.HasPrefix(s.Name, b.Text+".") {
				val, _, _ := v.h.Options().Get(s.Name, registry.At{})
				lines = append(lines, fmt.Sprintf("- %s = %v: %s", s.Name, val, s.Help))
			}
		}
		if len(lines) > 0 {
			return "Settings:\n" + strings.Join(lines, "\n")
		}
	case "api":
		for _, f := range api.Reference[strings.TrimPrefix(strings.TrimPrefix(b.Text, "oriel"), ".")] {
			lines = append(lines, fmt.Sprintf("- `%s`: %s", f.Call, f.Doc))
		}
		return strings.Join(lines, "\n")
	}
	return ""
}
