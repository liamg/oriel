package app

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/liamg/oriel/catalog"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// PluginsKeyMode is the Plugins page's keymap mode.
const PluginsKeyMode = "plugins"

const pluginsHelp = "plugins/install"

// The Plugins page's tabs.
const (
	tabInstalled = iota
	tabUpdates
	tabRegistry
)

// pluginRow is one plugin as the page lists it: built in, or from [plugins].
type pluginRow struct {
	id, name, version string
	badge             string // builtin, reviewed, trusted, unverified
	source            string // where it came from: builtin, a registry, a git URL, a path
	runs              string // BUILT-IN, NATIVE, LUA, PROCESS, WASM, DATA
	state             string // loaded, waiting for consent, disabled, failed, revoked
	st                *catalog.Status
	m                 plugin.Manifest
}

// pluginsPage is the page's state, kept by builtin:catalog.
type pluginsPage struct {
	open   bool
	tab    int
	sel    int
	filter string
	asked  bool // the Registry tab asked for the indexes
}

// pageCommands are the page's commands and its view.
func (x *catalogPlugin) pageCommands(h plugin.Host) {
	open := func(tab string) {
		x.page.open = true
		switch tab {
		case "updates":
			x.page.tab = tabUpdates
		case "registry":
			x.page.tab = tabRegistry
		case "installed":
			x.page.tab = tabInstalled
		}
	}
	for _, c := range []registry.Command{
		{ID: "Plugins", Title: "The Plugins page; :Plugins {installed, updates or registry}", Run: func(_ registry.Ctx, a registry.Args) error {
			open(strings.TrimSpace(a.String("args")))
			return nil
		}},
		{ID: "plugins.open", Title: "Open the Plugins page at a {tab}: installed, updates or registry", Run: func(_ registry.Ctx, a registry.Args) error {
			open(a.String("tab"))
			return nil
		}},
		{ID: "plugins.disable", Title: "Turn off {id}, a plugin bundled with Oriel", Run: func(ctx registry.Ctx, a registry.Args) error {
			return x.turnBundled(ctx, a.String("id"), false)
		}},
		{ID: "plugins.enable", Title: "Turn {id}, a plugin bundled with Oriel, back on", Run: func(ctx registry.Ctx, a registry.Args) error {
			return x.turnBundled(ctx, a.String("id"), true)
		}},
	} {
		x.add(h.Command(c))
	}
	x.add(h.Contribute(registry.StartEntry{Name: "plugins", Icon: "plugins", Title: "Plugins", Desc: "What's installed, updates, and the plugin registries",
		Command: "plugins.open", Order: 96, Link: true}))
	x.add(h.View(OverlayPrefix+"plugins", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "plugins", Fn: x.pageView}
	})))
}

// turnBundled turns a bundled plugin on or off, in your settings' bundled.disabled.
func (x *catalogPlugin) turnBundled(ctx registry.Ctx, id string, on bool) error {
	if x.bundled[id] == nil {
		return fmt.Errorf("%q isn't bundled with Oriel: uninstall it instead", id)
	}
	v, _, _ := x.h.Options().Get(BundledDisabledOption, registry.At{})
	off, _ := v.([]string)
	off = slices.DeleteFunc(slices.Clone(off), func(s string) bool { return s == id })
	if !on {
		off = append(off, id)
	}
	return x.h.Run(ctx, "settings.set", registry.Args{"name": BundledDisabledOption, "value": off, "scope": "user"})
}

// rows are what's installed: the built-ins, the bundled plugins, then every [plugins] entry.
func (x *catalogPlugin) rows() []pluginRow {
	var out []pluginRow
	if x.sh.active != nil {
		for _, p := range *x.sh.active {
			m := p.Manifest()
			if _, ok := p.(*scriptPlugin); ok || m.ID == "" {
				continue
			}
			runs := "BUILT-IN"
			if slices.Contains(plugin.Native(), p) {
				runs = "NATIVE"
			}
			out = append(out, pluginRow{id: m.ID, name: m.ID, version: m.Version, badge: catalog.BadgeBuiltin, source: "builtin", runs: runs,
				state: "loaded", m: m})
		}
	}
	slices.SortFunc(out, func(a, b pluginRow) int { return strings.Compare(a.id, b.id) })
	for _, id := range x.bundledIDs {
		bp := x.bundled[id]
		runs := "LUA"
		if len(bp.b.Processes) > 0 {
			runs = "PROCESS"
		}
		state := bp.state
		if state == "replaced" {
			state = "replaced by its install"
		}
		out = append(out, pluginRow{id: id, name: pluginName(id), version: bp.m.Version, badge: catalog.BadgeBundled, source: "bundled with Oriel",
			runs: runs, state: state, m: bp.m})
	}
	for _, st := range x.Statuses() {
		st := st
		r := pluginRow{id: st.Entry.ID, name: pluginName(st.Entry.ID), version: st.Manifest.Version, badge: st.Locked.Badge, source: st.Locked.Source,
			st: &st, m: st.Manifest, runs: runsKind(st.Dir, st.Manifest)}
		switch st.State {
		case catalog.Ready:
			r.state = "loaded"
		case catalog.Consent:
			r.state = "waiting for consent"
		case catalog.Disabled:
			r.state = "disabled"
		case catalog.Revoked:
			r.state = "revoked"
		default:
			r.state = "failed"
		}
		if r.source == "" {
			r.source = st.Entry.Source()
		}
		out = append(out, r)
	}
	return out
}

// runsKind is how a plugin runs, by what it ships: a process, WASM, Lua, or data alone.
func runsKind(dir string, m plugin.Manifest) string {
	switch {
	case dir == "":
		return "–"
	case len(m.Binaries) > 0:
		return "PROCESS"
	}
	wasm := false
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".wasm") {
			wasm = true
			return filepath.SkipAll
		}
		return nil
	})
	if wasm {
		return "WASM"
	}
	if fs, _ := plugin.Files(dir, plugin.EntryFile); len(fs) > 0 {
		return "LUA"
	}
	return "DATA"
}

// contributes counts what a plugin registered, by kind: its commands, options, and contributions of each kind; and
// what its manifest's [contributes] declares.
func contributes(r registry.Registry, id string, m plugin.Manifest) []string {
	mine := func(src string) bool {
		return src == id || src == "builtin:"+id || strings.HasPrefix(src, id+"/")
	}
	var out []string
	n := 0
	for _, c := range r.Commands() {
		if mine(c.Source) {
			n++
		}
	}
	if n > 0 {
		out = append(out, counted(n, "command"))
	}
	n = 0
	for _, o := range r.Options().Specs() {
		if mine(o.Source) {
			n++
		}
	}
	if n > 0 {
		out = append(out, counted(n, "option"))
	}
	for _, kind := range []string{"agent.tool", "agent.pack", "inbox.source", "hud.indicator", "ticket.tracker", "start.entry", "font", "icon"} {
		n := 0
		for _, c := range r.Contributions(kind) {
			if v := reflect.ValueOf(c); v.Kind() == reflect.Struct {
				if f := v.FieldByName("Source"); f.IsValid() && f.Kind() == reflect.String && mine(f.String()) {
					n++
				}
			}
		}
		if n > 0 {
			out = append(out, fmt.Sprintf("%d %s", n, kind))
		}
	}
	for _, p := range m.Contributes.Patterns() {
		if !slices.Contains(out, p[0]) {
			out = append(out, p[0])
		}
	}
	return out
}

func (x *catalogPlugin) pageView(c *ui.Ctx) {
	if !x.page.open || c.Theme().IsProbe() {
		return
	}
	if x.page.tab == tabRegistry && !x.page.asked {
		x.page.asked = true
		x.mu.Lock()
		fetched := x.client != nil
		x.mu.Unlock()
		if !fetched {
			go x.refresh()
		}
	}
	widget.Page(c, "plugins", &x.page.open, widget.PageOpts{Over: mainArea(), KeyMode: PluginsKeyMode, Label: "Plugins page", Help: pluginsHelp},
		func(c *ui.Ctx) {
			th := c.Theme()
			rows := x.rows()
			x.pageHeader(c, rows)
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			switch x.page.tab {
			case tabRegistry:
				x.registryTab(c, rows)
			default:
				shown := rows
				if x.page.tab == tabUpdates {
					shown = slices.DeleteFunc(slices.Clone(rows), func(r pluginRow) bool {
						return r.st == nil || (r.st.Update == "" && r.st.State != catalog.Consent)
					})
				}
				shown = x.filtered(shown)
				c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
					x.installedList(c, shown)
					c.Box("divider", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
					c.Column("detail", ui.Style{W: ui.Px(420), H: ui.Flex(1), ScrollY: true, Pad: ui.Pad(18), Gap: 12, Fill: th.C(theme.BgSurface)},
						func(c *ui.Ctx) {
							if x.page.sel >= 0 && x.page.sel < len(shown) {
								x.detail(c, shown[x.page.sel])
							}
						})
				})
			}
			c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(28), Pad: ui.PadXY(18, 0), Cross: ui.Center}, func(c *ui.Ctx) {
				loaded, ups := 0, 0
				for _, r := range rows {
					if r.state == "loaded" {
						loaded++
					}
					if r.st != nil && r.st.Update != "" {
						ups++
					}
				}
				c.Text("t", fmt.Sprintf("%d installed · %d running · %s", len(rows), loaded, counted(ups, "update")), monoFont(10.5, th.C(theme.FgFaint)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("file", "~/.config/oriel/settings.toml", monoFont(10.5, th.C(theme.FgFaint)))
			})
		})
}

func (x *catalogPlugin) filtered(rows []pluginRow) []pluginRow {
	q := strings.ToLower(strings.TrimSpace(x.page.filter))
	if q == "" {
		return rows
	}
	return slices.DeleteFunc(slices.Clone(rows), func(r pluginRow) bool {
		hay := strings.ToLower(strings.Join(append([]string{r.id, r.badge, r.runs, r.m.Description}, plugin.Locked(r.m.Permissions)...), " "))
		return !strings.Contains(hay, q)
	})
}

func (x *catalogPlugin) pageHeader(c *ui.Ctx, rows []pluginRow) {
	th := c.Theme()
	ups := 0
	for _, r := range rows {
		if r.st != nil && (r.st.Update != "" || r.st.State == catalog.Consent) {
			ups++
		}
	}
	c.Row("top", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(18, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("h", "Plugins", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 18, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		tabs := []string{fmt.Sprintf("INSTALLED %d", len(rows)), fmt.Sprintf("UPDATES %d", ups), "REGISTRY"}
		if widget.Tabs(c, "tabs", tabs, &x.page.tab, widget.TabsOpts{Help: pluginsHelp}) {
			x.page.sel = 0
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		c.Box("filter", ui.Style{W: ui.Px(300)}, func(c *ui.Ctx) {
			widget.Input(c, "q", &x.page.filter, widget.InputOpts{Label: "Filter plugins", Placeholder: "filter by name, kind, permission…", Help: pluginsHelp})
		})
		if widget.Button(c, "close", "Close", widget.ButtonOpts{Key: "esc", Help: pluginsHelp}) {
			x.page.open = false
		}
	})
}

func (x *catalogPlugin) installedList(c *ui.Ctx, rows []pluginRow) {
	th := c.Theme()
	c.Column("list", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Row("heads", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 18, R: 18, T: 10, B: 6}, Gap: 12}, func(c *ui.Ctx) {
			for i, h := range []struct {
				t string
				w ui.Size
			}{{"PLUGIN", ui.Px(260)}, {"CONTRIBUTES", ui.Flex(1)}, {"PERMISSIONS", ui.Flex(1)}, {"RUNS", ui.Px(90)}} {
				c.Box(i, ui.Style{W: h.w, Clip: true}, func(c *ui.Ctx) { c.Text("t", h.t, capText(th.C(theme.FgFaint))) })
			}
		})
		c.Column("rows", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true}, func(c *ui.Ctx) {
			if len(rows) == 0 {
				c.Row("none", ui.Style{Pad: ui.PadXY(18, 12)}, func(c *ui.Ctx) { note(c, "t", "Nothing here.") })
				return
			}
			widget.Rows(c, "plugins", len(rows), &x.page.sel, widget.RowsOpts{Label: "Plugins", KeyMode: PluginsKeyMode, Help: pluginsHelp, ClickSelects: true,
				RowLabel: func(i int) string { return rows[i].id }}, func(c *ui.Ctx, i int, selected bool) {
				x.rowView(c, rows[i], selected)
			})
		})
	})
}

func (x *catalogPlugin) rowView(c *ui.Ctx, r pluginRow, selected bool) {
	th := c.Theme()
	st := ui.Style{W: ui.Flex(1), Pad: ui.PadXY(18, 9), Gap: 12, Cross: ui.Center}
	if selected {
		st.Fill = th.C(theme.BgRaised)
	}
	c.Row(r.id, st, func(c *ui.Ctx) {
		c.Row("name", ui.Style{W: ui.Px(260), Gap: 10, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
			c.Row("icon", ui.Style{W: ui.Px(26), H: ui.Px(26), Radius: 6, Fill: th.C(theme.BgRaised), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("t", strings.ToUpper(r.name[:1]), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12, Weight: typeset.Bold}, Color: th.C(theme.Fg)})
			})
			c.Column("t", ui.Style{Gap: 3, Clip: true}, func(c *ui.Ctx) {
				c.Row("l1", ui.Style{Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Text("n", r.name, uiFont(13, th.C(theme.Fg)))
					if r.version != "" {
						c.Text("v", r.version, monoFont(10, th.C(theme.FgFaint)))
					}
					if r.badge != catalog.BadgeBuiltin {
						widget.Chip(c, "badge", strings.ToUpper(r.badge), th.C(badgeTone(r.badge)), pluginsHelp+"#badges")
					}
				})
				c.Text("l2", r.id+" · "+r.state, monoFont(10, th.C(theme.FgFaint)))
			})
		})
		c.Row("contrib", ui.Style{W: ui.Flex(1), Gap: 6, Clip: true}, func(c *ui.Ctx) {
			for i, k := range contributes(x.h, r.id, r.m) {
				widget.Chip(c, i, strings.ToUpper(k), th.C(theme.FgMuted), pluginsHelp)
			}
		})
		c.Box("perms", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			perms := strings.Join(plugin.Locked(r.m.Permissions), " · ")
			if perms == "" {
				perms = "none"
			}
			c.Text("t", perms, monoFont(10.5, th.C(theme.FgMuted)))
		})
		c.Box("runs", ui.Style{W: ui.Px(90), Clip: true}, func(c *ui.Ctx) { c.Text("t", r.runs, capText(th.C(theme.Accent))) })
	})
}

// detail is the selected plugin: its manifest, what it's granted, where it's from, and what can be done with it.
func (x *catalogPlugin) detail(c *ui.Ctx, r pluginRow) {
	th := c.Theme()
	c.Row("head", ui.Style{W: ui.Flex(1), Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Column("t", ui.Style{W: ui.Flex(1), Gap: 4, Clip: true}, func(c *ui.Ctx) {
			c.Text("n", r.name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 16, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			meta := []string{r.id}
			if r.version != "" {
				meta = append(meta, r.version)
			}
			meta = append(meta, strings.ToLower(r.runs))
			c.Text("m", strings.Join(meta, " · "), monoFont(10.5, th.C(theme.FgFaint)))
		})
		widget.Chip(c, "badge", strings.ToUpper(r.badge), th.C(badgeTone(r.badge)), pluginsHelp+"#badges")
	})
	if r.m.Description != "" {
		widget.Wrap(c, "desc", r.m.Description, 380, uiFont(12.5, th.C(theme.FgMuted)))
	}
	section := func(key, title string, lines [][2]string) {
		c.Column(key, ui.Style{W: ui.Flex(1), Gap: 5}, func(c *ui.Ctx) {
			c.Text("h", title, kickerFont(th.C(theme.FgFaint)))
			for i, l := range lines {
				c.Row(i, ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
					c.Text("k", l[0], monoFont(10.5, th.C(theme.FgFaint)))
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					c.Box("v", ui.Style{Clip: true}, func(c *ui.Ctx) { c.Text("t", l[1], monoFont(10.5, th.C(theme.Fg))) })
				})
			}
		})
	}
	var man [][2]string
	man = append(man, [2]string{"runs", runsOf(r.m)}, [2]string{"source", r.source})
	if r.st != nil && r.st.Locked.Commit != "" {
		man = append(man, [2]string{"commit", r.st.Locked.Commit[:min(12, len(r.st.Locked.Commit))]})
	}
	for _, f := range contributes(x.h, r.id, r.m) {
		man = append(man, [2]string{"contributes", f})
	}
	if !r.m.Lazy.Empty() {
		man = append(man, [2]string{"lazy", strings.Join(append(append(r.m.Lazy.Commands, r.m.Lazy.Events...), r.m.Lazy.Keys...), ", ")})
	}
	section("manifest", "MANIFEST · CONTRIBUTIONS", man)
	var granted [][2]string
	for _, l := range plugin.Locked(r.m.Permissions) {
		mark := "✓ granted"
		if r.st != nil && r.st.State == catalog.Consent {
			mark = "? waiting"
		}
		granted = append(granted, [2]string{l, mark})
	}
	if len(granted) == 0 {
		granted = [][2]string{{"ui, themes, notifications", "always"}}
	}
	section("granted", "GRANTED", granted)
	if r.badge == catalog.BadgeBundled {
		c.Row("acts", ui.Style{W: ui.Flex(1), Gap: 8, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
			cmd, label := "plugins.disable", "Disable"
			if r.state == "disabled" {
				cmd, label = "plugins.enable", "Enable"
			}
			if widget.Button(c, "turn", label, widget.ButtonOpts{Help: pluginsHelp + "#bundled"}) {
				if err := x.h.Run(context.Background(), cmd, registry.Args{"id": r.id}); err != nil {
					x.h.Notify(noticeErr("plugins", "Plugins", err))
				}
			}
		})
	}
	if r.st != nil {
		var prov [][2]string
		for _, f := range r.st.Facts {
			prov = append(prov, [2]string{f.Label, f.Value})
		}
		if r.st.Entry.Project != "" {
			prov = append(prov, [2]string{"project", r.st.Entry.Project})
		}
		if len(prov) > 0 {
			section("prov", "PROVENANCE", prov)
		}
		if r.st.Warning != "" {
			widget.Wrap(c, "warn", "⚠ "+r.st.Warning, 380, uiFont(12, th.C(tConsentWarning)))
		}
		if r.st.Err != nil {
			widget.Wrap(c, "err", r.st.Err.Error(), 380, uiFont(12, th.C(theme.Error)))
		}
		c.Row("acts", ui.Style{W: ui.Flex(1), Gap: 8, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
			id, scope := r.id, scopeOf(r.st.Entry)
			run := func(cmd string, a registry.Args) {
				go func() {
					if err := x.h.Run(context.Background(), cmd, a); err != nil {
						x.h.Notify(noticeErr("plugins", "Plugins", err))
					}
				}()
			}
			if r.st.State == catalog.Consent && widget.Button(c, "review", "Review", widget.ButtonOpts{Primary: true, Help: pluginsHelp}) {
				run("plugins.review", registry.Args{"id": id})
			}
			if r.st.Update != "" && widget.Button(c, "update", "Update to "+r.st.Update, widget.ButtonOpts{Primary: true, Help: pluginsHelp}) {
				run("plugins.update", registry.Args{"id": id})
			}
			if widget.Button(c, "uninstall", "Uninstall", widget.ButtonOpts{Help: pluginsHelp}) {
				run("plugins.uninstall", registry.Args{"id": id, "scope": scope})
			}
		})
	}
}

// registryTab lists every plugin the registries offer, each with Install, which writes its line to [plugins].
func (x *catalogPlugin) registryTab(c *ui.Ctx, installed []pluginRow) {
	th := c.Theme()
	x.mu.Lock()
	client := x.client
	x.mu.Unlock()
	c.Column("registry", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.PadXY(18, 12), Gap: 4}, func(c *ui.Ctx) {
		if client == nil {
			note(c, "wait", "Fetching the registries…")
			return
		}
		for _, s := range client.Sources {
			if err := client.Err(s.Repo); err != nil {
				widget.Wrap(c, "err"+s.Repo, s.Repo+": "+err.Error(), 900, uiFont(12, th.C(theme.Error)))
			}
		}
		all := client.All()
		q := strings.ToLower(strings.TrimSpace(x.page.filter))
		all = slices.DeleteFunc(all, func(f catalog.Found) bool {
			return q != "" && !strings.Contains(strings.ToLower(f.ID+" "+f.Description), q)
		})
		if len(all) == 0 {
			note(c, "none", "The registries list no plugins.")
		}
		for _, f := range all {
			x.registryRow(c, f, slices.ContainsFunc(installed, func(r pluginRow) bool { return r.id == f.ID }))
		}
	})
}

func (x *catalogPlugin) registryRow(c *ui.Ctx, f catalog.Found, installed bool) {
	th := c.Theme()
	badge, newest := catalog.BadgeReviewed, ""
	if f.Trusted {
		badge = catalog.BadgeTrusted
	}
	vs := []string{}
	for _, v := range f.Versions {
		if v.Yanked == "" && f.Revoked[v.Version] == "" {
			vs = append(vs, v.Version)
		}
	}
	if len(vs) > 0 {
		sortVersionsDesc(vs)
		newest = vs[0]
	}
	c.Row(f.ID, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(0, 8), Gap: 12, Cross: ui.Center, Label: f.ID, Role: ui.RoleGroup}, func(c *ui.Ctx) {
		c.Column("t", ui.Style{W: ui.Flex(1), Gap: 3, Clip: true}, func(c *ui.Ctx) {
			c.Row("l1", ui.Style{Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("n", f.ID, uiFont(13, th.C(theme.Fg)))
				if newest != "" {
					c.Text("v", newest, monoFont(10, th.C(theme.FgFaint)))
				}
				widget.Chip(c, "badge", strings.ToUpper(badge), th.C(badgeTone(badge)), pluginsHelp+"#badges")
			})
			where := f.Registry
			if len(f.Also) > 0 {
				where += " (also in " + strings.Join(f.Also, ", ") + ")"
			}
			c.Text("l2", strings.TrimSpace(f.Description+" · "+where), monoFont(10, th.C(theme.FgFaint)))
		})
		if installed {
			c.Text("in", "installed", capText(th.C(theme.FgFaint)))
			return
		}
		if widget.Button(c, "install", "Install", widget.ButtonOpts{Help: pluginsHelp}) {
			id := f.ID
			go func() {
				if err := x.h.Run(context.Background(), "plugins.install", registry.Args{"id": id}); err != nil {
					x.h.Notify(noticeErr("plugins", "Install "+id, err))
				}
			}()
		}
	})
}

func sortVersionsDesc(vs []string) {
	slices.SortFunc(vs, func(a, b string) int {
		x, xe := plugin.ParseVersion(a)
		y, ye := plugin.ParseVersion(b)
		if xe != nil || ye != nil {
			return strings.Compare(b, a)
		}
		return y.Compare(x)
	})
}

// noticeErr is a failed action's notice.
func noticeErr(id, title string, err error) core.Notice {
	return core.Notice{ID: id, Severity: core.SevError, Title: title, Text: err.Error()}
}
