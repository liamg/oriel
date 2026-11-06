package settings

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The Prompts page: every prompt the user can edit (agent/prompts) on the
// left, the selected one's text on the right. Only what they change is kept, a file each under the config dir; the
// session host gets them at start and on every change.
const (
	PromptsKeyMode     = "prompts"
	PromptsTextKeyMode = "prompts.text"
	promptsHelp        = "agent/prompts"
)

type Prompts struct {
	kit.Disposer
	ConfigDir string
	h         plugin.Host
	sending   sync.Mutex // one send to the session host at a time, each of the prompts as they are then
	busy      kit.Busy   // the sends

	open   bool
	sel    string
	drafts map[string]string // what's in the box, by prompt, while it differs from the prompt
	focus  bool
	err    string
	saved  string            // the prompt just saved, for its word
	o      prompts.Overrides // the user's versions, as last read
}

func (x *Prompts) Manifest() plugin.Manifest { return plugin.Manifest{ID: "prompts"} }

func (x *Prompts) store() prompts.Store {
	return prompts.Store{Dir: x.ConfigDir, Registered: kit.Prompts(x.h)}
}

func (x *Prompts) Activate(_ context.Context, h plugin.Host) error {
	x.Add(h.Contribute(registry.Busy{Name: "prompts", Busy: x.busy.Busy, Source: "builtin:prompts"}))
	x.h, x.drafts = h, map[string]string{}
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	for _, k := range x.keymaps() {
		x.Add(h.Keymap(k))
	}
	x.Add(h.Hook(registry.Hook{Event: "BufWritePost", Label: "send the prompts to the session host", Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
		if w, ok := ev.(*kit.BufWritePost); ok && x.isPromptFile(w.File) {
			x.changed()
		}
		return registry.Verdict{}, nil
	}}))
	x.Add(h.Contribute(kit.PagePlace("prompts", "Prompts", "prompts.open", &x.open)))
	x.Add(h.Contribute(uireg.SettingsSection{Name: "prompts", Build: x.section, Source: "builtin:prompts"}))
	x.Add(h.View(kit.OverlayPrefix+"prompts", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return &ui.Func{Key: "prompts", Fn: x.page} })))
	x.changed() // the session host has the user's prompts from the start
	return nil
}

// section is the settings page's way to the Prompts page.
func (x *Prompts) section(c *ui.Ctx) {
	widget.Wrap(c, "about", "The coding style, and what each kind of session tells its agent.", 700, kit.UIFont(12.5, c.Theme().C(theme.FgFaint)))
	c.Row("open", ui.Style{Pad: ui.Insets{T: 8}}, func(c *ui.Ctx) {
		if widget.Button(c, "open", "Open Prompts", widget.ButtonOpts{Primary: true, Help: "agent/prompts"}) {
			x.openPage("")
		}
	})
}

func (x *Prompts) commands() []registry.Command {
	return []registry.Command{
		{ID: "Prompts", Title: "The Prompts page; :Prompts {id}", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.openPage(strings.TrimSpace(a.String("args")))
		}},
		{ID: "prompts.open", Title: "Open the Prompts page, at a prompt by {id}: style.code, pack.work, part.drafts…",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.openPage(a.String("id")) }},
		{ID: "prompts.set", Title: "Save your version of a prompt: {id}, {text}; empty text or the default goes back to the default",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.set(x.target(a), a.String("text")) }},
		{ID: "prompts.reset", Title: "Put a prompt back to its default: {id}, the selected one by default",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.set(x.target(a), "") }},
		{ID: "prompts.save", Title: "Save the Prompts page's text for the selected prompt", Available: func(registry.Args) bool { return x.open },
			Run: func(_ registry.Ctx, a registry.Args) error {
				id := x.target(a)
				d, ok := x.drafts[id]
				if !ok {
					return nil
				}
				return x.set(id, d)
			}},
		{ID: "prompts.edit", Title: "Edit a prompt's file in the editor: {id}, the selected one by default; made from the default the first time",
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.edit(ctx, x.target(a)) }},
	}
}

func (x *Prompts) keymaps() []registry.Keymap {
	return []registry.Keymap{
		{Mode: PromptsKeyMode, Keys: "e", Command: "prompts.edit", Desc: "Edit in the editor"},
		{Mode: PromptsTextKeyMode, Keys: "<D-s>", Command: "prompts.save", Desc: "Save"},
		{Mode: PromptsTextKeyMode, Keys: "<C-s>", Command: "prompts.save", Desc: "Save"},
	}
}

func (x *Prompts) Deactivate(ctx context.Context) error {
	x.busy.Wait()
	return x.Disposer.Deactivate(ctx)
}

// isPromptFile reports whether path is where a prompt's version is kept.
func (x *Prompts) isPromptFile(path string) bool {
	for _, d := range prompts.Defs(kit.Prompts(x.h)...) {
		if filepath.Clean(path) == filepath.Join(x.ConfigDir, d.File) {
			return true
		}
	}
	return false
}

func (x *Prompts) openPage(id string) error {
	x.open, x.err, x.saved = true, "", ""
	x.o = x.store().Load()
	if id != "" {
		if _, ok := prompts.Lookup(id, kit.Prompts(x.h)...); !ok {
			return fmt.Errorf("no prompt %q", id)
		}
		x.sel, x.focus = id, true
	}
	return nil
}

// target is the prompt {id}, or the one selected.
func (x *Prompts) target(a registry.Args) string {
	if id := a.String("id"); id != "" {
		return id
	}
	return x.selected()
}

func (x *Prompts) selected() string {
	if _, ok := prompts.Lookup(x.sel, kit.Prompts(x.h)...); ok {
		return x.sel
	}
	return prompts.StyleID
}

func (x *Prompts) set(id, text string) error {
	if err := x.store().Set(id, text); err != nil {
		x.err = err.Error()
		return err
	}
	delete(x.drafts, id)
	x.err, x.saved = "", id
	x.changed()
	return nil
}

// changed reads the prompts again and sends them to the session host.
func (x *Prompts) changed() {
	x.o = x.store().Load()
	w, err := kit.SessionHost(x.h)
	if err != nil {
		return
	}
	x.busy.Go(func() {
		x.sending.Lock()
		defer x.sending.Unlock()
		if err := w.SetPrompts(x.store().Load()); err != nil {
			x.h.Log().Warn("prompts", "err", err)
		}
	})
}

func (x *Prompts) edit(ctx registry.Ctx, id string) error {
	path, err := x.store().Path(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		d, _ := prompts.Lookup(id, kit.Prompts(x.h)...)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(d.Default+"\n"), 0o644); err != nil {
			return err
		}
	}
	x.open = false
	return x.h.Run(ctx, "editor.open", registry.Args{"path": path})
}

// The page.

func (x *Prompts) page(c *ui.Ctx) {
	if !x.open || c.Theme().IsProbe() {
		return
	}
	o := x.o
	defs := prompts.Defs(kit.Prompts(x.h)...)
	widget.Page(c, "prompts", &x.open, widget.PageOpts{KeyMode: PromptsKeyMode, Label: "Prompts page", Help: promptsHelp + "#page"},
		func(c *ui.Ctx) {
			th := c.Theme()
			x.header(c, o)
			c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				x.list(c, defs, o)
				c.Box("divider", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
				c.Column("detail", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 18, R: 18, T: 14, B: 18}, Gap: 10,
					Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
					if d, ok := prompts.Lookup(x.selected(), kit.Prompts(x.h)...); ok {
						x.detail(c, d, o)
					}
				})
			})
		})
}

func (x *Prompts) header(c *ui.Ctx, o prompts.Overrides) {
	th := c.Theme()
	c.Row("top", ui.Style{W: ui.Flex(1), H: ui.Px(42), Pad: ui.PadXY(18, 0), Gap: 12, Cross: ui.Center, Fill: th.C(kit.TStatsHead)}, func(c *ui.Ctx) {
		c.Text("h", "PROMPTS", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.SemiBold, Tracking: .04},
			Color: th.C(theme.Fg)})
		c.Text("m", fmt.Sprintf("%s · %d yours", kit.Counted(len(prompts.Defs(kit.Prompts(x.h)...)), "prompt"), len(o)), kit.MonoFont(11, th.C(theme.FgFaint)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if x.err != "" {
			c.Text("err", x.err, kit.MonoFont(11, th.C(theme.Error)))
		}
		if widget.Button(c, "close", "Close", widget.ButtonOpts{Key: "esc", Help: promptsHelp + "#page"}) {
			x.open = false
		}
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

// promptRow is a row of the list: a group's heading, or a prompt.
type promptRow struct {
	group string
	def   prompts.Def
}

func promptRows(defs []prompts.Def) []promptRow {
	var out []promptRow
	group := ""
	for _, d := range defs {
		if d.Group != group {
			group = d.Group
			out = append(out, promptRow{group: group})
		}
		out = append(out, promptRow{def: d})
	}
	return out
}

func (x *Prompts) list(c *ui.Ctx, defs []prompts.Def, o prompts.Overrides) {
	th := c.Theme()
	rows := promptRows(defs)
	cur := x.selected()
	c.Column("list", ui.Style{W: ui.Px(280), H: ui.Flex(1), Fill: th.C(theme.BgSurface)}, func(c *ui.Ctx) {
		c.Column("rows", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
			sel := 0
			for i, r := range rows {
				if r.def.ID == cur {
					sel = i
				}
			}
			res := widget.Rows(c, "prompts", len(rows), &sel, widget.RowsOpts{Label: "Prompts list", KeyMode: PromptsKeyMode, Help: promptsHelp + "#list",
				Static:   func(i int) bool { return rows[i].group != "" },
				RowLabel: func(i int) string { return cmp.Or(rows[i].def.Label, rows[i].group) }}, func(c *ui.Ctx, i int, _ bool) {
				r := rows[i]
				if r.group != "" {
					c.Row("g", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 14, R: 14, T: 10, B: 4}}, func(c *ui.Ctx) {
						c.Text("t", strings.ToUpper(r.group), kit.KickerFont(th.C(theme.FgFaint)))
					})
					return
				}
				x.rowView(c, r.def, r.def.ID == cur, o.Custom(r.def.ID))
			})
			if (res.Changed || res.Activated >= 0) && sel < len(rows) && rows[sel].def.ID != "" && rows[sel].def.ID != x.sel {
				x.sel, x.err, x.saved = rows[sel].def.ID, "", ""
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Column("foot", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 10), Gap: 3}, func(c *ui.Ctx) {
			kit.Note(c, "a", "yours: "+kit.TildePath(x.ConfigDir))
			kit.Note(c, "b", "only what you change is kept")
		})
	})
}

func (x *Prompts) rowView(c *ui.Ctx, d prompts.Def, on, custom bool) {
	th := c.Theme()
	fill, edge := render.Color{}, render.Color{}
	if on {
		fill, edge = th.C(kit.TStatsRowOn), th.C(theme.Accent)
	}
	c.Row("row", ui.Style{W: ui.Flex(1), Fill: fill}, func(c *ui.Ctx) {
		c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
		c.Row("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 14, T: 7, B: 7}, Gap: 8, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
			c.Text("t", d.Label, kit.UIFont(13, th.C(theme.Fg)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if custom {
				c.Text("yours", "yours", kit.MonoFont(10, th.C(theme.Accent)))
			}
		})
	})
}

func (x *Prompts) detail(c *ui.Ctx, d prompts.Def, o prompts.Overrides) {
	th := c.Theme()
	help := promptsHelp + "#prompt"
	custom := o.Custom(d.ID)
	c.Row("title", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("t", d.Label, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 17, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		c.Text("id", d.ID, kit.MonoFont(11, th.C(theme.FgGhost)))
		if custom {
			widget.Chip(c, "yours", "yours", th.C(theme.Accent), help)
		} else {
			widget.Chip(c, "default", "default", th.C(theme.FgMuted), help)
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if widget.Button(c, "edit", "Open in editor", widget.ButtonOpts{Key: "e", Help: help}) {
			x.run("prompts.edit", registry.Args{"id": d.ID})
		}
		if widget.Button(c, "reset", "Reset to default", widget.ButtonOpts{Ghost: true, Disabled: !custom, Help: help}) {
			x.run("prompts.reset", registry.Args{"id": d.ID})
		}
	})
	widget.Wrap(c, "help", d.Help, 760, kit.UIFont(12, th.C(theme.FgMuted)))
	if len(d.Vars) > 0 {
		c.Column("vars", ui.Style{W: ui.Flex(1), Gap: 3}, func(c *ui.Ctx) {
			for _, v := range d.Vars {
				c.Text(v.Name, "{{"+v.Name+"}} "+v.Desc, kit.MonoFont(10.5, th.C(theme.FgFaint)))
			}
		})
	}
	text, editing := x.drafts[d.ID]
	if !editing {
		text = o.Text(d.ID, kit.Prompts(x.h)...)
	}
	if x.focus {
		c.RequestFocus(c.Next("text"))
		x.focus = false
	}
	res := widget.Prose(c, "text", &text, widget.ProseOpts{Label: d.Label + " text", Lines: 28, MinLines: 12, KeyMode: PromptsTextKeyMode, Help: help})
	if res.Changed {
		if text == o.Text(d.ID, kit.Prompts(x.h)...) {
			delete(x.drafts, d.ID)
		} else {
			x.drafts[d.ID] = text
		}
		x.saved = ""
	}
	c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Main: ui.End}, func(c *ui.Ctx) {
		switch {
		case editing:
			kit.Note(c, "state", "not saved")
		case x.saved == d.ID:
			kit.Note(c, "state", "saved · sessions use it from now on")
		}
		path, _ := x.store().Path(d.ID)
		kit.Note(c, "file", kit.TildePath(path))
		if widget.Button(c, "save", "Save", widget.ButtonOpts{Primary: true, Key: "⌘S", Disabled: !editing, Help: help}) {
			x.run("prompts.save", registry.Args{"id": d.ID})
		}
	})
}

func (x *Prompts) run(id string, a registry.Args) {
	x.err = ""
	if err := x.h.Run(context.Background(), id, a); err != nil {
		x.err = err.Error()
		x.h.Notify(core.Notice{ID: "prompts", Severity: core.SevError, Text: err.Error()})
	}
}
