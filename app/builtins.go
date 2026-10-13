package app

import (
	"context"
	"fmt"
	"path/filepath"
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
	sh.layout = &layoutExt{}
	return []ext.Extension{&uiOptions{}, &themes{dir: filepath.Join(sh.cfg.ConfigDir, "themes")}, sh.layout, &introspect{configDir: sh.cfg.ConfigDir}, &statusLine{}, &sessionTabs{sh: sh}, &omnibox{}, &picker{}, &whichKey{}, &helpView{sh: sh}, &notices{sh: sh},
		&editorExt{sh: sh}}
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
	l.panes.Title = func(v layout.ViewRef) string {
		if view, ok := h.ViewByID(v.ID); ok {
			if t, ok := view.(registry.Titled); ok {
				return t.Title(v.Instance)
			}
		}
		return ""
	}
	str := func(a registry.Args, k string) string { s, _ := a[k].(string); return s }
	focused := func() (layout.ViewRef, error) {
		if l.root.Focus == nil {
			return layout.ViewRef{}, fmt.Errorf("no view has focus")
		}
		return *l.root.Focus, nil
	}
	cmds := []registry.Command{
		{ID: "view.open", Title: "Open a view", Run: func(_ registry.Ctx, a registry.Args) error {
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
		{ID: "view.close", Title: "Close a view: the focused one, or {id, instance}", Run: func(_ registry.Ctx, a registry.Args) error {
			if id := str(a, "id"); id != "" {
				l.root.Close(layout.ViewRef{ID: id, Instance: str(a, "instance")})
				return nil
			}
			v, err := focused()
			if err == nil {
				l.root.Close(v)
			}
			return err
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
	c.Box("move", ui.Style{Z: 1500, Float: &ui.Float{Point: &at}}, func(c *ui.Ctx) {
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

// introspect is :Keys and :Commands: every keymap and command, with where it came from.
type introspect struct {
	disposer
	configDir string
}

func (i *introspect) Manifest() ext.Manifest { return ext.Manifest{ID: "introspect"} }

func (i *introspect) Activate(_ context.Context, h ext.Host) error {
	open := func(view string) func(registry.Ctx, registry.Args) error {
		return func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "view.open", registry.Args{"id": view})
		}
	}
	i.add(h.Picker(commandsSource(h)))
	i.add(h.Picker(keymapsSource(h, i.configDir)))
	i.add(h.Command(registry.Command{ID: "Keys", Title: "List keymaps", Run: open("keys")}))
	i.add(h.Command(registry.Command{ID: "Commands", Title: "List commands", Run: open("commands")}))
	i.add(h.View("keys", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		rows := []ui.Node{ui.TextNode(fmt.Sprintf("%-10s %-14s %-24s %s", "MODE", "KEYS", "COMMAND", "SOURCE"))}
		for _, k := range h.Keymaps() {
			rows = append(rows, ui.TextNode(fmt.Sprintf("%-10s %-14s %-24s %s", k.Mode, k.Keys, k.Command, k.Source)))
		}
		return ui.ColumnNode(rows...)
	})))
	i.add(h.View("commands", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		rows := []ui.Node{ui.TextNode(fmt.Sprintf("%-24s %-30s %s", "COMMAND", "TITLE", "SOURCE"))}
		for _, c := range h.Commands() {
			rows = append(rows, ui.TextNode(fmt.Sprintf("%-24s %-30s %s", c.ID, c.Title, c.Source)))
		}
		return ui.ColumnNode(rows...)
	})))
	return nil
}

// uiOptions registers the toolkit's own options.
type uiOptions struct{ disposer }

func (o *uiOptions) Manifest() ext.Manifest { return ext.Manifest{ID: "ui"} }

func (o *uiOptions) Activate(_ context.Context, h ext.Host) error {
	o.add(h.Option(registry.OptionSpec{Name: "ui.reduced_motion", Type: registry.Bool, Default: false,
		Scopes: registry.ScopeUser, Help: "Jump straight to the end of every animation."}))
	o.add(h.Option(registry.OptionSpec{Name: "ui.theme", Type: registry.String, Default: "oriel",
		Scopes: registry.ScopeUser | registry.ScopeProject, Help: "The theme, by name: a built-in, or a file in your themes directory."}))
	o.add(h.Option(registry.OptionSpec{Name: "ui.variant", Type: registry.Enum, Values: []string{"dark", "light"}, Default: "dark",
		Scopes: registry.ScopeUser | registry.ScopeProject, Help: "The theme's dark or light variant."}))
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
	return nil
}
