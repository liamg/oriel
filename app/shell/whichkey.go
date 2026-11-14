package shell

import (
	"context"
	"fmt"
	"github.com/liamg/oriel/ui/widget"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// WhichKeyDelay is how long a prefix waits before which-key shows what can follow it.
const WhichKeyDelay = 300 * time.Millisecond

var (
	tWKKey   = theme.Declare("whichkey.key", "accent")
	tWKGroup = theme.Declare("whichkey.group", "status.permission")
	tWKDesc  = theme.Declare("whichkey.desc", "fg")
)

// whichKey is the which-key popup: after a pause on a prefix, every key that can come next, with its
// description, from the registry's keymaps and the engine's own commands. Built only on the public API.
type whichKey struct {
	kit.Disposer
	h plugin.Host
}

func (w *whichKey) Manifest() plugin.Manifest { return plugin.Manifest{ID: "whichkey"} }

func (w *whichKey) Activate(_ context.Context, h plugin.Host) error {
	w.h = h
	w.Add(h.Option(registry.OptionSpec{Name: "whichkey.enabled", Type: registry.Bool, Default: true, Scopes: registry.ScopeUser,
		Help: "Show what keys can follow a prefix, after a short pause."}))
	w.Add(h.View(kit.OverlayPrefix+"whichkey", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "whichkey", Fn: w.build}
	})))
	return nil
}

// entry is a key that can come next: a mapping, a group of them, or an engine's own command.
type entry struct {
	key, desc, source string
	group             bool
	count             int
}

// entries are what can follow keys in modes: each keymap one key longer (or a group of longer ones, under the
// label a keymap with no command gives it), then the engine's own hints for keys no mapping takes.
func entries(h plugin.Host, p plugin.Pending) (title string, out []entry) {
	prefix := keymap.Format(p.Keys)
	n := len(p.Keys)
	byKey := map[string]*entry{}
	var order []string
	for _, k := range h.Keymaps() {
		if !slices.Contains(p.Modes, k.Mode) {
			continue
		}
		toks := keymap.Parse(k.Keys)
		if len(toks) <= n || keymap.Format(toks[:n]) != prefix {
			if k.Command == "" && keymap.Format(toks) == prefix {
				title = k.Desc
			}
			continue
		}
		next := toks[n]
		e, ok := byKey[next]
		if !ok {
			e = &entry{key: next}
			byKey[next] = e
			order = append(order, next)
		}
		switch {
		case len(toks) == n+1 && k.Command == "":
			e.group, e.desc = true, k.Desc
		case len(toks) == n+1:
			e.desc, e.source = k.Desc, k.Source
			if e.desc == "" {
				e.desc = k.Command
			}
		default:
			e.group = true
			e.count++
		}
	}
	for _, hint := range p.Hints {
		if _, ok := byKey[hint.Keys]; !ok {
			byKey[hint.Keys] = &entry{key: hint.Keys, desc: hint.Desc, source: "builtin"}
			order = append(order, hint.Keys)
		}
	}
	if p.Runs != "" {
		byKey["<CR>"] = &entry{key: "<CR>", desc: p.Runs}
		order = append(order, "<CR>")
	}
	for _, k := range order {
		e := byKey[k]
		if e.group && e.desc == "" {
			e.desc = "+more"
		}
		out = append(out, *e)
	}
	slices.SortStableFunc(out, func(a, b entry) int { return strings.Compare(strings.ToLower(a.key), strings.ToLower(b.key)) })
	return title, out
}

func (w *whichKey) build(c *ui.Ctx) {
	if on, _, err := w.h.Options().Get("whichkey.enabled", registry.At{}); err == nil && on == false {
		return
	}
	p := w.h.Pending()
	if len(p.Keys) == 0 {
		return
	}
	if due := p.Since.Add(WhichKeyDelay); c.Now().Before(due) {
		c.WakeAt(due)
		return
	}
	title, es := entries(w.h, p)
	if len(es) == 0 {
		return
	}
	w.h.HoldPending()
	th := c.Theme()
	size := c.Size()
	at := render.Point{X: size.X / 2, Y: size.Y - 40}
	mono := func(sz float32, tok theme.TokenID) ui.TextStyle {
		return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: sz}, Color: th.C(tok)}
	}
	c.Column("wk", ui.Style{W: ui.Px(min(1180, size.X-32)), Fill: th.C(kit.TOmniBg), Stroke: th.C(kit.TOmniBorder), Border: 1, Radius: 10, Clip: true, Z: ui.LayerOverlay,
		Float: &ui.Float{Point: &at, Self: render.Point{X: .5, Y: 1}}, Role: ui.RoleDialog, Label: "Keys that can follow"}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(44), Pad: ui.PadXY(16, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			for i, k := range p.Keys {
				c.Box(i, ui.Style{}, func(c *ui.Ctx) { kit.KbdKey(c, kit.KeyLabel(k)) })
			}
			if title != "" {
				c.Text("title", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold}, Color: th.C(tWKGroup)})
			}
			c.Text("how", fmt.Sprintf("shown after %dms · ⌫ back up · ? all · esc cancel", WhichKeyDelay.Milliseconds()), mono(10, theme.FgFaint))
		})
		widget.Rule(c, "rule")
		cols := max(1, min(4, int(min(1180, size.X-32)/280)))
		rows := (len(es) + cols - 1) / cols
		c.Row("grid", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(8, 8), Gap: 8}, func(c *ui.Ctx) {
			for col := range cols {
				c.Column(col, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
					for r := range rows {
						i := col*rows + r
						if i >= len(es) {
							break
						}
						e := es[i]
						c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(28), Pad: ui.PadXY(10, 0), Gap: 10, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
							c.Text("k", kit.KeyLabel(e.key), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 12, Weight: typeset.SemiBold}, Color: th.C(tWKKey)})
							c.Text("arrow", "→", mono(11, theme.FgGhost))
							desc := tWKDesc
							if e.group {
								desc = tWKGroup
							}
							c.Text("d", e.desc, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5}, Color: th.C(desc)})
							c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
							switch {
							case e.group && e.count > 0:
								c.Text("n", fmt.Sprint(e.count), mono(10, theme.FgGhost))
							case e.source != "" && !strings.HasPrefix(e.source, "builtin"):
								c.Text("src", e.source, mono(10, theme.FgGhost))
							}
						})
					}
				})
			}
		})
	})
}
