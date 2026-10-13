package app

import (
	"context"
	"fmt"
	"github.com/liamg/oriel/render/typeset"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/ui/widget"
)

func builtins(sh *shell) []ext.Extension {
	sh.layout = &layoutExt{sh: sh}
	return []ext.Extension{&uiOptions{}, &languagesExt{dir: filepath.Join(sh.cfg.ConfigDir, "languages"), cacheDir: sh.cfg.CacheDir}, &themes{dir: filepath.Join(sh.cfg.ConfigDir, "themes")}, sh.layout, &gitExt{sh: sh}, &rail{}, &explorer{sh: sh}, &start{sh: sh}, &overview{sh: sh}, &introspect{configDir: sh.cfg.ConfigDir}, &statusLine{}, &sessionTabs{sh: sh}, &omnibox{}, &picker{}, &whichKey{}, &helpView{sh: sh}, &contextMenus{sh: sh}, &notices{sh: sh}, &problems{sh: sh}, &themeEditor{dir: filepath.Join(sh.cfg.ConfigDir, "themes")},
		&editorExt{sh: sh}, &gitSigns{sh: sh}, &projectSearch{sh: sh}, &lspExt{sh: sh}, &settingsExt{sh: sh}, &luaExt{sh: sh, dir: sh.cfg.ConfigDir}}
}

// disposer is the usual way for an extension to undo what it registered.
type disposer []registry.Disposable

func (d *disposer) add(x registry.Disposable) { *d = append(*d, x) }

func (d *disposer) Deactivate(context.Context) error {
	for _, x := range *d {
		x.Dispose()
	}
	*d = nil
	return nil
}

// layoutExt is the layout every view lives in (ui.md 01d), and the commands and ⌃W keys that move views
// around it. view.open is how anything, built-in or plugin, puts a view on screen.
type layoutExt struct {
	disposer
	sh    *shell
	root  *layout.Root
	panes layout.Panes
	rects map[*layout.Node]render.Rect
	saved map[string]layout.Snapshot
	menu  bool
}

func (l *layoutExt) Manifest() ext.Manifest { return ext.Manifest{ID: "layout"} }

var movePlacements = []struct {
	label, keys string
	args        registry.Args
}{
	{"Dock bottom", "^W J", registry.Args{"place": "dock", "edge": "bottom"}},
	{"Split in main, right", "^W L", registry.Args{"place": "split", "side": "right"}},
	{"Tab in main", "^W T", registry.Args{"place": "tab"}},
	{"Float", "^W F", registry.Args{"place": "float"}},
	{"Zoom (toggle)", "^W Z", nil},
	{"Drop-down (quake)", "^`", registry.Args{"place": "dropdown"}},
}

func (l *layoutExt) Activate(_ context.Context, h ext.Host) error {
	l.root, l.saved = layout.New(), map[string]layout.Snapshot{}
	l.panes.Empty = func(c *ui.Ctx) {
		id := StartView
		if l.sh != nil && l.sh.cfg.WorkDir != "" {
			id = OverviewView
		}
		if v, ok := h.ViewByID(id); ok {
			c.Column(id, ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) { c.Node(v.Build(registry.ViewCtx{})) })
		}
	}
	l.panes.Title = func(v layout.ViewRef) string {
		if view, ok := h.ViewByID(v.ID); ok {
			if t, ok := view.(registry.Titled); ok {
				return t.Title(v.Instance)
			}
		}
		return ""
	}
	l.panes.Mark = func(v layout.ViewRef) (string, render.Color) {
		view, ok := h.ViewByID(v.ID)
		if !ok {
			return "", render.Color{}
		}
		m, ok := registry.As[registry.Marked](view)
		if !ok {
			return "", render.Color{}
		}
		text, style := m.Mark(v.Instance)
		if text == "" {
			return "", render.Color{}
		}
		return text, tokenColour(h.CurrentTheme(), style, h.CurrentTheme().C(theme.FgFaint))
	}
	str := func(a registry.Args, k string) string { s, _ := a[k].(string); return s }
	focused := func() (layout.ViewRef, error) {
		if l.root.Focus == nil {
			return layout.ViewRef{}, fmt.Errorf("no view has focus")
		}
		return *l.root.Focus, nil
	}
	viewRegistered := func(a registry.Args) bool {
		id := str(a, "id")
		if id == "" {
			return true
		}
		_, ok := h.ViewByID(id)
		return ok
	}
	cmds := []registry.Command{
		{ID: "view.open", Title: "Open a view", Available: viewRegistered, Run: func(_ registry.Ctx, a registry.Args) error {
			id := str(a, "id")
			if _, ok := h.ViewByID(id); !ok {
				return fmt.Errorf("no view %q", id)
			}
			place := layout.Placement(str(a, "place"))
			if place == "" {
				place = layout.Tab
			}
			size, _ := a["size"].(float64)
			if n, ok := a["size"].(int); ok {
				size = float64(n)
			}
			o := layout.Opts{Edge: layout.Edge(str(a, "edge")), Side: layout.Side(str(a, "side")), Size: float32(size)}
			if b := str(a, "beside"); b != "" {
				id, inst, _ := strings.Cut(b, "#")
				o.Beside = &layout.ViewRef{ID: id, Instance: inst}
			}
			return l.root.Open(layout.ViewRef{ID: id, Instance: str(a, "instance"), Title: str(a, "title")}, place, o)
		}},
		{ID: "view.toggle", Title: "Show a view {id}, or close it if it's showing", Available: viewRegistered, Run: func(ctx registry.Ctx, a registry.Args) error {
			v := layout.ViewRef{ID: str(a, "id"), Instance: str(a, "instance")}
			for _, s := range l.root.Visible() {
				if s.ID == v.ID && (v.Instance == "" || s.Instance == v.Instance) {
					l.root.Close(s)
					return nil
				}
			}
			return h.Run(ctx, "view.open", a)
		}},
		{ID: "view.close", Title: "Close a view: the focused one, or {id, instance}; with {docked}, only one outside the main area", Run: func(_ registry.Ctx, a registry.Args) error {
			if id := str(a, "id"); id != "" {
				l.root.Close(layout.ViewRef{ID: id, Instance: str(a, "instance")})
				return nil
			}
			v, err := focused()
			if err != nil {
				return err
			}
			if w := l.root.Find(v); a["docked"] == true && (w == nil || w.Placement == layout.Tab) {
				return nil
			}
			l.root.Close(v)
			return nil
		}},
		{ID: "layout.move", Title: "Move the focused view", Run: func(_ registry.Ctx, a registry.Args) error {
			v, err := focused()
			if err != nil {
				return err
			}
			return l.root.Move(v, layout.Placement(str(a, "place")), layout.Opts{Edge: layout.Edge(str(a, "edge")), Side: layout.Side(str(a, "side"))})
		}},
		{ID: "layout.zoom", Title: "Zoom the focused view (toggle)", Run: func(registry.Ctx, registry.Args) error {
			v, err := focused()
			if err == nil {
				l.root.ToggleZoom(v)
			}
			return err
		}},
		{ID: "layout.focus", Title: "Focus the view in a direction", Run: func(_ registry.Ctx, a registry.Args) error {
			l.root.FocusDir(layout.Dir(str(a, "dir")), l.rects)
			return nil
		}},
		{ID: "layout.dock", Title: "Toggle a dock", Run: func(_ registry.Ctx, a registry.Args) error {
			l.root.ToggleDock(layout.Edge(str(a, "edge")))
			return nil
		}},
		{ID: "layout.equalize", Title: "Even out the splits", Run: func(registry.Ctx, registry.Args) error {
			l.root.Equalize()
			return nil
		}},
		{ID: "layout.save", Title: "Save the layout by name", Run: func(_ registry.Ctx, a registry.Args) error {
			l.saved[str(a, "name")] = l.root.Save()
			return nil
		}},
		{ID: "layout.restore", Title: "Restore a saved layout", Run: func(_ registry.Ctx, a registry.Args) error {
			s, ok := l.saved[str(a, "name")]
			if !ok {
				return fmt.Errorf("no layout %q", str(a, "name"))
			}
			r, err := layout.Restore(s)
			if err == nil {
				l.root = r
			}
			return err
		}},
		{ID: "layout.menu", Title: "Move the focused view…", Run: func(registry.Ctx, registry.Args) error {
			l.menu = !l.menu
			return nil
		}},
	}
	for _, c := range cmds {
		l.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Keys: "<C-w>J", Command: "layout.move", Args: registry.Args{"place": "dock", "edge": "bottom"}},
		{Keys: "<C-w>L", Command: "layout.move", Args: registry.Args{"place": "split", "side": "right"}},
		{Keys: "<C-w>T", Command: "layout.move", Args: registry.Args{"place": "tab"}},
		{Keys: "<C-w>F", Command: "layout.move", Args: registry.Args{"place": "float"}},
		{Keys: "<C-w>Z", Command: "layout.zoom"},
		{Keys: "<C-w>=", Command: "layout.equalize"},
		{Keys: "<C-w>M", Command: "layout.menu"},
		{Keys: "<C-w>h", Command: "layout.focus", Args: registry.Args{"dir": "h"}},
		{Keys: "<C-w>j", Command: "layout.focus", Args: registry.Args{"dir": "j"}},
		{Keys: "<C-w>k", Command: "layout.focus", Args: registry.Args{"dir": "k"}},
		{Keys: "<C-w>l", Command: "layout.focus", Args: registry.Args{"dir": "l"}},
		{Keys: "<C-j>", Command: "layout.dock", Args: registry.Args{"edge": "bottom"}},
		{Keys: "<C-`>", Command: "layout.move", Args: registry.Args{"place": "dropdown"}},
	} {
		k.Mode = "global"
		l.add(h.Keymap(k))
	}
	for _, k := range []registry.Keymap{
		{Keys: "q", Command: "view.close", Desc: "Close"},
		{Keys: "<C-w>c", Command: "view.close", Desc: "Close"},
		{Keys: "<C-w>q", Command: "view.close", Desc: "Close"},
		{Keys: "<Esc>", Command: "view.close", Args: registry.Args{"docked": true}, Desc: "Close a docked view"},
	} {
		k.Mode = ViewKeyMode
		l.add(h.Keymap(k))
	}
	l.add(h.View(RootView, registry.ViewFunc(func(vc registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "layout", Fn: func(c *ui.Ctx) {
			l.rects = l.panes.RectsIn(c)
			layout.View(c, "panes", l.root, &l.panes, func(c *ui.Ctx, v layout.ViewRef, focused bool) {
				view, ok := h.ViewByID(v.ID)
				if !ok {
					c.Text("missing", "no view "+v.ID, ui.TextStyle{})
					return
				}
				pad := ui.Pad(10)
				if _, edge := registry.As[registry.EdgeToEdge](view); edge {
					pad = ui.Insets{}
				}
				vc := vc
				vc.Instance, vc.Focused = v.Instance, focused
				c.Column("view", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: pad}, func(c *ui.Ctx) {
					c.Node(view.Build(vc))
				})
			})
			if l.menu {
				l.moveMenu(c, h)
			}
		}}
	})))
	return nil
}

// moveMenu is ⌃W M: the placements, with their keys (24-layout).
func (l *layoutExt) moveMenu(c *ui.Ctx, h ext.Host) {
	items := make([]widget.MenuItem, len(movePlacements))
	for i, p := range movePlacements {
		items[i] = widget.MenuItem{Label: p.label, Shortcut: p.keys, Help: layout.HelpPane}
	}
	at := render.Point{X: c.Size().X - 340, Y: 40}
	menu := ui.Child(c.Next("move"), "menu")
	if c.Interaction(menu).Keys == nil {
		c.Focus(menu)
	}
	c.Box("move", ui.Style{Z: ui.LayerPopup, Float: &ui.Float{Point: &at}}, func(c *ui.Ctx) {
		chosen, closed := widget.Menu(c, "menu", items, widget.MenuOpts{Width: 320, Help: layout.HelpPane})
		switch {
		case closed:
			l.menu = false
		case chosen >= 0:
			l.menu = false
			p := movePlacements[chosen]
			if p.args == nil {
				h.Run(context.Background(), "layout.zoom", nil)
			} else {
				h.Run(context.Background(), "layout.move", p.args)
			}
		}
	})
}

// introspect is :Keys and :Commands: every keymap and command, with where it came from, in a picker.
type introspect struct {
	disposer
	configDir string
}

func (i *introspect) Manifest() ext.Manifest { return ext.Manifest{ID: "introspect"} }

func (i *introspect) Activate(_ context.Context, h ext.Host) error {
	pick := func(source string) func(registry.Ctx, registry.Args) error {
		return func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "picker.open", registry.Args{"source": source})
		}
	}
	i.add(h.Picker(commandsSource(h)))
	i.add(h.Picker(keymapsSource(h, i.configDir)))
	i.add(h.Command(registry.Command{ID: "Keys", Title: "List keymaps", Run: pick("keymaps")}))
	i.add(h.Command(registry.Command{ID: "Commands", Title: "List commands", Run: pick("commands")}))
	return nil
}

// uiOptions registers the toolkit's own options.
type uiOptions struct{ disposer }

func (o *uiOptions) Manifest() ext.Manifest { return ext.Manifest{ID: "ui"} }

func (o *uiOptions) Activate(_ context.Context, h ext.Host) error {
	for _, c := range []registry.Command{
		{ID: "ui.focus.next", Title: "Move keyboard focus to the next control", Run: func(registry.Ctx, registry.Args) error { return o.focus(h, 1) }},
		{ID: "ui.focus.prev", Title: "Move keyboard focus to the previous control", Run: func(registry.Ctx, registry.Args) error { return o.focus(h, -1) }},
	} {
		o.add(h.Command(c))
	}
	for _, c := range []registry.Command{
		{ID: "ui.drag", Title: "Pick up what's focused to drag it, or drop what's held on it", Run: func(registry.Ctx, registry.Args) error {
			if u := h.UI(); u != nil {
				u.DragKey()
			}
			return nil
		}},
		{ID: "ui.drag.cancel", Title: "Put down what's being dragged", Run: func(registry.Ctx, registry.Args) error {
			if u := h.UI(); u != nil {
				u.CancelDrag()
			}
			return nil
		}},
	} {
		o.add(h.Command(c))
	}
	for _, mode := range []string{"button", ui.DragKeyMode} {
		o.add(h.Keymap(registry.Keymap{Mode: mode, Keys: "<Tab>", Command: "ui.focus.next", Desc: "Next control"}))
		o.add(h.Keymap(registry.Keymap{Mode: mode, Keys: "<S-Tab>", Command: "ui.focus.prev", Desc: "Previous control"}))
	}
	o.add(h.Keymap(registry.Keymap{Mode: ui.DragKeyMode, Keys: "<Space>", Command: "ui.drag", Desc: "Pick up or drop"}))
	o.add(h.Keymap(registry.Keymap{Mode: ui.DragKeyMode, Keys: "<Esc>", Command: "ui.drag.cancel", Desc: "Put down"}))
	o.add(h.Option(registry.OptionSpec{Name: "ui.reduced_motion", Type: registry.Bool, Default: false,
		Scopes: registry.ScopeUser, Help: "Jump straight to the end of every animation."}))
	o.add(h.Option(registry.OptionSpec{Name: "ui.theme", Type: registry.String, Default: "oriel", Choices: func() []string { return themeNames(h) },
		Scopes: registry.ScopeUser | registry.ScopeProject, Help: "The theme, by name: a built-in, or a file in your themes directory. :Themes previews them."}))
	o.add(h.Option(registry.OptionSpec{Name: "ui.variant", Type: registry.Enum, Values: []string{"system", "dark", "light"}, Default: "system",
		Scopes: registry.ScopeUser | registry.ScopeProject, Help: "The theme's dark or light variant; system follows the OS."}))
	return nil
}

func (o *uiOptions) focus(h ext.Host, dir int) error {
	if u := h.UI(); u != nil {
		u.FocusNext(dir)
	}
	return nil
}

// themes registers the built-in themes and the theme files in the user's themes directory.
type themes struct {
	disposer
	dir string
}

func (t *themes) Manifest() ext.Manifest { return ext.Manifest{ID: "themes"} }

func (t *themes) Activate(_ context.Context, h ext.Host) error {
	for _, s := range theme.Builtins() {
		t.add(h.Theme(s))
	}
	files, _ := filepath.Glob(filepath.Join(t.dir, "*.toml"))
	for _, f := range files {
		t.add(h.Theme(theme.File{Path: f}))
	}
	t.add(h.Picker(themePicker(h)))
	t.add(h.Command(registry.Command{ID: "Themes", Title: "Pick a theme, each shown as it's highlighted", Run: func(ctx registry.Ctx, _ registry.Args) error {
		return h.Run(ctx, "picker.open", registry.Args{"source": "themes"})
	}}))
	t.add(h.Keymap(registry.Keymap{Mode: "n", Keys: "<Leader>T", Command: "picker.open", Args: registry.Args{"source": "themes"}, Desc: "Themes"}))
	return nil
}

// themeSwatches are a theme's main colours, in the variant in use, for the picker's preview.
func themeSwatches(h ext.Host, name string) ui.Node {
	variant := theme.Dark
	if cur := h.CurrentTheme(); cur != nil {
		variant = cur.Variant
	}
	srcs, _ := h.Themes()
	r, err := resolveTheme(srcs, name, variant)
	return &ui.Func{Key: "swatches", Fn: func(c *ui.Ctx) {
		th := c.Theme()
		mono := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgMuted)}
		c.Column("swatches", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
			c.Text("name", strings.ToUpper(name)+" · "+string(variant), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold}, Color: th.C(theme.FgFaint)})
			if err != nil {
				c.Text("err", err.Error(), ui.TextStyle{Font: mono.Font, Color: th.C(theme.Error)})
				return
			}
			sample := strings.TrimRight(themeSample, "\n")
			code := highlightAround(h, "sample.go", []byte(sample), 1, 0, strings.Count(sample, "\n"))
			code.title, code.theme, code.size, code.fill = "sample.go", r, 13.5, true
			code.build(c, nil)
			swatches := []struct {
				tok  theme.TokenID
				name string
			}{{theme.BgBase, "bg.base"}, {theme.BgSurface, "bg.surface"}, {theme.BgRaised, "bg.raised"}, {theme.Fg, "fg"},
				{theme.FgMuted, "fg.muted"}, {theme.Accent, "accent"}, {theme.Brand, "brand"}, {theme.OK, "ok"}, {theme.Warn, "warn"},
				{theme.Error, "error"}}
			c.Row("grid", ui.Style{W: ui.Flex(1), Gap: 24}, func(c *ui.Ctx) {
				for col := range 2 {
					c.Column(col, ui.Style{Gap: 8}, func(c *ui.Ctx) {
						for i, sw := range swatches[col*5 : col*5+5] {
							c.Row(i, ui.Style{Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
								c.Box("sw", ui.Style{W: ui.Px(44), H: ui.Px(18), Radius: 4, Fill: r.C(sw.tok), Stroke: th.C(theme.LineStrong), Border: 1}, nil)
								c.Text("n", sw.name, mono)
							})
						}
					})
				}
			})
		})
	}}
}

// themeSample is the code a theme's preview highlights: keywords, types, strings, numbers, comments and calls.
const themeSample = `package pricing

import (
	"errors"
	"fmt"
	"time"
)

// ErrNoTier is returned for a tier nobody has heard of.
var ErrNoTier = errors.New("pricing: no such tier")

// Tier is a plan's price, in pence, and when it changed.
type Tier struct {
	Name    string
	Pence   int
	Changed time.Time
}

// PriceFor is a tier's price for qty seats, with 10% off from 100.
func PriceFor(t *Tier, qty int) (int, error) {
	if t == nil {
		return 0, ErrNoTier
	}
	if qty <= 0 {
		return 0, fmt.Errorf("bad quantity %d", qty)
	}
	total := t.Pence * qty
	if qty >= 100 {
		total = total * 9 / 10 // volume discount
	}
	return total, nil
}
`

// themeNames are the registered themes' names, sorted.
func themeNames(h registry.Registry) []string {
	srcs, _ := h.Themes()
	var out []string
	for _, src := range srcs {
		if !slices.Contains(out, src.Name()) {
			out = append(out, src.Name())
		}
	}
	slices.Sort(out)
	return out
}

// themePicker is :Themes: each theme shows as it's highlighted, Enter keeps it in your settings, and Esc goes back
// to the one before.
func themePicker(h ext.Host) registry.PickerSource {
	type prev struct {
		v      any // the user layer's
		had    bool
		source string
		cur    any // what was in use
	}
	var before *prev // the user layer's ui.theme before the first preview
	o := h.Options()
	restore := func() {
		if before == nil {
			return
		}
		if before.had {
			o.SetFrom("ui.theme", registry.LayerUser, registry.At{}, before.v, before.source)
		} else {
			o.Unset("ui.theme", registry.LayerUser, registry.At{})
		}
		before = nil
	}
	return registry.PickerSource{ID: "themes", Title: "Themes", Order: 80, PickerOnly: true, Source: "builtin:themes",
		Items: func(registry.Ctx, string) []registry.PickerItem {
			cur, _, _ := o.Get("ui.theme", registry.At{})
			if before != nil {
				cur = before.cur
			}
			var out []registry.PickerItem
			for _, n := range themeNames(h) {
				it := registry.PickerItem{ID: n, Label: n, Current: n == cur}
				if it.Current {
					it.Note = "current"
				}
				out = append(out, it)
			}
			return out
		},
		Hover: func(it registry.PickerItem) {
			if before == nil {
				v, had := o.Lookup("ui.theme", registry.LayerUser, registry.At{})
				cur, l, _ := o.Get("ui.theme", registry.At{})
				src := ""
				if l == registry.LayerUser {
					src = o.Source("ui.theme", registry.At{})
				}
				before = &prev{v, had, src, cur}
			}
			o.Set("ui.theme", registry.LayerUser, registry.At{}, it.ID)
		},
		Cancel:  restore,
		Preview: func(it registry.PickerItem) ui.Node { return themeSwatches(h, it.ID) },
		Run: func(ctx registry.Ctx, it registry.PickerItem) error {
			restore()
			return h.Run(ctx, "settings.set", registry.Args{"name": "ui.theme", "value": it.ID, "scope": "user"})
		},
	}
}
