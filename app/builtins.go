package app

import (
	"context"
	"fmt"
	"github.com/liamg/oriel/render/typeset"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/ui/widget"
)

func builtins(sh *shell) []plugin.Plugin {
	perms := &permissionsPlugin{sh: sh}
	sh.layout = &layoutPlugin{sh: sh}
	return []plugin.Plugin{&uiOptions{}, &languagesPlugin{dir: filepath.Join(sh.cfg.ConfigDir, "languages"), cacheDir: sh.cfg.CacheDir}, &themes{dir: filepath.Join(sh.cfg.ConfigDir, "themes")}, sh.layout, &gitPlugin{sh: sh}, &rail{sh: sh}, &explorer{sh: sh}, &start{sh: sh}, &overview{sh: sh}, &introspect{configDir: sh.cfg.ConfigDir}, &statusLine{sh: sh}, &sessionTabs{sh: sh}, &hudPlugin{sh: sh}, &inboxPlugin{sh: sh}, &missionPlugin{sh: sh}, &omnibox{}, &picker{}, &whichKey{}, &consentPlugin{}, &helpView{sh: sh}, &navPlugin{sh: sh}, &contextMenus{sh: sh}, &notices{sh: sh}, &problems{sh: sh}, &themeEditor{dir: filepath.Join(sh.cfg.ConfigDir, "themes")},
		&editorPlugin{sh: sh}, &markdownPlugin{}, &imagePlugin{}, &desktopPlugin{sh: sh}, &gitSigns{sh: sh}, &projectSearch{sh: sh}, &lspPlugin{sh: sh}, &testsPlugin{sh: sh}, &debugPlugin{sh: sh}, &explainPlugin{sh: sh}, &walkthroughPlugin{sh: sh}, &terminals{sh: sh}, &settingsPlugin{sh: sh}, &codeSitesPlugin{sh: sh}, &sessions{sh: sh}, &agentConsole{sh: sh}, perms, &workspacePlugin{sh: sh, perms: perms}, &statsPlugin{sh: sh}, &jobsPlugin{sh: sh}, &reposPlugin{sh: sh}, &machinePlugin{sh: sh}, &updatePlugin{sh: sh}, &promptsPlugin{sh: sh}, &voicePlugin{sh: sh}, &assistantPlugin{sh: sh}, &ticketsPlugin{}, &reviewsPlugin{sh: sh}, &githubHost{sh: sh}, &launcher{sh: sh}, &requests{sh: sh}, &planPlugin{sh: sh}, &diffPlugin{sh: sh}, &shipPlugin{sh: sh}, &sessionEnd{sh: sh}, &aiReviewPlugin{sh: sh}, &verifyPlugin{sh: sh}, &githubPlugin{sh: sh}, &secretsPlugin{sh: sh}, &luaPlugin{sh: sh, dir: sh.cfg.ConfigDir}, &catalogPlugin{sh: sh}}
}

// onLoop runs fn on the app's loop and waits for it: an agent tool's way in from the session host's goroutine.
func onLoop(ctx context.Context, h plugin.Host, fn func()) error {
	done := make(chan struct{})
	h.Do(func() { fn(); close(done) })
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// disposer is the usual way for a plugin to undo what it registered.
type disposer []registry.Disposable

func (d *disposer) add(x registry.Disposable) { *d = append(*d, x) }

// onDispose is a func as a Disposable: a subscription's cancel.
type onDispose func()

func (f onDispose) Dispose() { f() }

func (d *disposer) Deactivate(context.Context) error {
	for _, x := range *d {
		x.Dispose()
	}
	*d = nil
	return nil
}

// layoutPlugin is the layout every view lives in (ui.md 01d), and the commands and ⌃W keys that move views
// around it. view.open is how anything, built-in or plugin, puts a view on screen.
type layoutPlugin struct {
	disposer
	sh    *shell
	root  *layout.Root
	panes layout.Panes
	rects map[*layout.Node]render.Rect
	saved map[string]layout.Snapshot
	menu  bool

	restored bool
	kept     []byte
	writer   stateWriter
}

func (l *layoutPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "layout"} }

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

func (l *layoutPlugin) Activate(_ context.Context, h plugin.Host) error {
	l.root, l.saved = layout.New(), map[string]layout.Snapshot{}
	// A view put in front is what the user asked to see: the page over the layout gives way to it.
	l.root.OnShow(func(layout.ViewRef) { closePlaces(h) })
	h.Do(func() { // once every plugin has registered its views
		l.restore(h)
		l.restored = true
		if l.sh != nil && l.sh.afterOpen != nil && l.sh.afterOpen.session == h.State().Focus {
			calls := l.sh.afterOpen.calls
			l.sh.afterOpen = nil
			runCalls(h, calls)
		}
	})
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
	l.panes.Lead = func(v layout.ViewRef) (render.Color, bool) {
		w := projectsHere(h)
		p, ok := viewProject(h, w, v)
		if !ok || !w.many() || h.CurrentTheme() == nil {
			return render.Color{}, false
		}
		return w.colour(h.CurrentTheme(), p.Name), true
	}
	l.panes.Tip = func(v layout.ViewRef) string {
		w := projectsHere(h)
		p, ok := viewProject(h, w, v)
		if !ok || !w.many() {
			return ""
		}
		if file := viewFile(h, v); file != "" {
			if rel, err := filepath.Rel(p.Dir, file); err == nil {
				return p.Name + " · " + filepath.ToSlash(rel)
			}
		}
		return p.Name
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
		{ID: "view.open", Title: "Open a view: {id, instance, place, edge, side, size, beside, title, project}; alone, in a dock, closes what was there; fixed, it can't be dragged away", Available: viewRegistered, Run: func(_ registry.Ctx, a registry.Args) error {
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
			o := layout.Opts{Edge: layout.Edge(str(a, "edge")), Side: layout.Side(str(a, "side")), Size: float32(size), Alone: a["alone"] == true,
				Fixed: a["fixed"] == true}
			if b := str(a, "beside"); b != "" {
				id, inst, _ := strings.Cut(b, "#")
				o.Beside = &layout.ViewRef{ID: id, Instance: inst}
			}
			return l.root.Open(layout.ViewRef{ID: id, Instance: str(a, "instance"), Title: str(a, "title"), Project: str(a, "project")}, place, o)
		}},
		{ID: "view.toggle", Title: "Show a view {id} (for a {project}), or close it if it's showing", Available: viewRegistered,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				if s, ok := l.matching(a, l.root.Visible()); ok {
					if placeShown(h) {
						// Open under a page: asked for, it's revealed, not put away.
						l.root.Show(s)
						return nil
					}
					l.root.Close(s)
					return nil
				}
				return h.Run(ctx, "view.open", a)
			}},
		{ID: "view.close", Title: "Close a view: the focused one, or {id, instance, project}; with {docked}, only one outside the main area", Run: func(_ registry.Ctx, a registry.Args) error {
			if id := str(a, "id"); id != "" {
				if v, ok := l.matching(a, l.root.All()); ok {
					l.root.Close(v)
				}
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
			return l.root.Load(s)
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
				vc.Instance, vc.Focused, vc.Project = v.Instance, focused, v.Project
				c.Column("view", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: pad}, func(c *ui.Ctx) {
					c.Node(view.Build(vc))
				})
			})
			if l.menu {
				l.moveMenu(c, h)
			}
			l.keep(h) // after the views, which take their keys as they're built
		}}
	})))
	return nil
}

// matching is the first of vs that's view {id}, of {instance} and {project} when they're given.
func (l *layoutPlugin) matching(a registry.Args, vs []layout.ViewRef) (layout.ViewRef, bool) {
	for _, v := range vs {
		if v.ID == a.String("id") && (a.String("instance") == "" || v.Instance == a.String("instance")) && v.Project == a.String("project") {
			return v, true
		}
	}
	return layout.ViewRef{}, false
}

// moveMenu is ⌃W M: the placements, with their keys (24-layout).
func (l *layoutPlugin) moveMenu(c *ui.Ctx, h plugin.Host) {
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

func (i *introspect) Manifest() plugin.Manifest { return plugin.Manifest{ID: "introspect"} }

func (i *introspect) Activate(_ context.Context, h plugin.Host) error {
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

// uiFontSize is ui.font_size's default: the size the mockups are drawn at, zoom 1.
const uiFontSize = 13.0

// partSizes are the parts with their own text size: each option scales its part over ui.font_size.
var partSizes = []struct{ name, help string }{
	{"ui.statusline.font_size", "The status line's text size; it follows ui.font_size until it's set."},
	{"ui.hover.font_size", "The text size of hovers and signature help; it follows ui.font_size until it's set."},
	{"ui.completion.font_size", "The completion menu's text size; it follows ui.font_size until it's set."},
	{"ui.tooltip.font_size", "Tooltips' text size; it follows ui.font_size until it's set."},
}

// partScale is what a part with its own size option is drawn at over the UI; 0 when it follows the UI.
func partScale(o registry.Options, name string) float32 {
	v, l, err := o.Get(name, registry.At{})
	if err != nil || l == registry.LayerDefault {
		return 0
	}
	if ui, _, err := o.Get("ui.font_size", registry.At{}); err == nil && ui.(float64) > 0 {
		return float32(v.(float64) / ui.(float64))
	}
	return float32(v.(float64) / uiFontSize)
}

// zoomOf is the window's zoom for ui.font_size (theming.md, "Sizes").
func zoomOf(o registry.Options) float32 {
	if v, _, err := o.Get("ui.font_size", registry.At{}); err == nil {
		return float32(v.(float64) / uiFontSize)
	}
	return 1
}

func (o *uiOptions) Manifest() plugin.Manifest { return plugin.Manifest{ID: "ui"} }

func (o *uiOptions) Activate(_ context.Context, h plugin.Host) error {
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
	o.add(h.Option(registry.OptionSpec{Name: "ui.font_size", Type: registry.Float, Default: uiFontSize, Min: 8, Max: 32, Step: 0.5,
		Scopes: registry.ScopeUser, Help: "The UI's text size. It zooms the whole window, so panels grow with their text; ⌘= and ⌘- step it, ⌘0 resets it."}))
	for _, p := range partSizes {
		o.add(h.Option(registry.OptionSpec{Name: p.name, Type: registry.Float, Default: uiFontSize, Min: 6, Max: 32, Step: 0.5, Follows: "ui.font_size",
			Scopes: registry.ScopeUser, Help: p.help}))
	}
	step := func(by float64) func(registry.Ctx, registry.Args) error {
		return func(registry.Ctx, registry.Args) error {
			opts := h.Options()
			if by == 0 {
				opts.Unset("ui.font_size", registry.LayerUser, registry.At{})
				return nil
			}
			v, _, err := opts.Get("ui.font_size", registry.At{})
			if err != nil {
				return err
			}
			return opts.Set("ui.font_size", registry.LayerUser, registry.At{}, min(32, max(8, v.(float64)+by)))
		}
	}
	for _, c := range []registry.Command{
		{ID: "ui.zoom.in", Title: "Make the UI bigger", Run: step(1)},
		{ID: "ui.zoom.out", Title: "Make the UI smaller", Run: step(-1)},
		{ID: "ui.zoom.reset", Title: "Put the UI back to its size", Run: step(0)},
	} {
		o.add(h.Command(c))
	}
	for keys, cmd := range map[string]string{"<D-=>": "ui.zoom.in", "<D-->": "ui.zoom.out", "<D-0>": "ui.zoom.reset"} {
		o.add(h.Keymap(registry.Keymap{Mode: "global", Keys: keys, Command: cmd}))
	}
	o.add(h.Option(registry.OptionSpec{Name: "ui.reduced_motion", Type: registry.Bool, Default: false,
		Scopes: registry.ScopeUser, Help: "Jump straight to the end of every animation."}))
	o.add(h.Option(registry.OptionSpec{Name: "ui.scrollbars", Type: registry.Enum, Values: []string{"auto", "always"}, Default: "auto",
		Scopes: registry.ScopeUser, Help: "When scrollbars show: auto, while the pointer is over what scrolls and for a moment after it scrolls; or always, whenever there's more than fits."}))
	o.add(h.Option(registry.OptionSpec{Name: "ui.theme", Type: registry.String, Default: "oriel", Choices: func() []string { return themeNames(h) },
		Scopes: registry.ScopeUser | registry.ScopeProject, Help: "The theme, by name: a built-in, or a file in your themes directory. :Themes previews them."}))
	o.add(h.Option(registry.OptionSpec{Name: "ui.variant", Type: registry.Enum, Values: []string{"system", "dark", "light"}, Default: "system",
		Scopes: registry.ScopeUser | registry.ScopeProject, Help: "The theme's dark or light variant; system follows the OS."}))
	return nil
}

func (o *uiOptions) focus(h plugin.Host, dir int) error {
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

func (t *themes) Manifest() plugin.Manifest { return plugin.Manifest{ID: "themes"} }

func (t *themes) Activate(_ context.Context, h plugin.Host) error {
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

// themePreviews are the theme picker's resolved themes and highlighted sample, kept until the themes or languages
// change or the picker closes.
type themePreviews struct {
	themes, langs int
	by            map[string]themePreview // by name and variant
	sample        *sourceFile
}

type themePreview struct {
	r   *theme.Resolved
	err error
}

func (p *themePreviews) of(h plugin.Host, name string, v theme.Variant) (*theme.Resolved, error, sourceFile) {
	srcs, themes := h.Themes()
	_, langs := h.Languages()
	if p.by == nil || p.themes != themes || p.langs != langs {
		*p = themePreviews{themes: themes, langs: langs, by: map[string]themePreview{}}
	}
	k := name + "\x00" + string(v)
	t, ok := p.by[k]
	if !ok {
		t.r, t.err = resolveTheme(srcs, name, v)
		p.by[k] = t
	}
	if p.sample == nil {
		sample := strings.TrimRight(themeSample, "\n")
		p.sample = highlightAround(h, "sample.go", []byte(sample), 1, 0, strings.Count(sample, "\n"))
	}
	return t.r, t.err, *p.sample
}

// themeSwatches are a theme's main colours, in the variant in use (or its own, if it has one), for the picker's preview.
func themeSwatches(h plugin.Host, p *themePreviews, name string) ui.Node {
	variant := theme.Dark
	if cur := h.CurrentTheme(); cur != nil {
		variant = cur.Variant
	}
	r, err, code := p.of(h, name, variant)
	return &ui.Func{Key: "swatches", Fn: func(c *ui.Ctx) {
		th := c.Theme()
		mono := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgMuted)}
		c.Column("swatches", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
			shown := variant
			if r != nil {
				shown = r.Variant
			}
			c.Text("name", strings.ToUpper(name)+" · "+string(shown), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold}, Color: th.C(theme.FgFaint)})
			if err != nil {
				c.Text("err", err.Error(), ui.TextStyle{Font: mono.Font, Color: th.C(theme.Error)})
				return
			}
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
func themePicker(h plugin.Host) registry.PickerSource {
	type prev struct {
		v      any // the user layer's
		had    bool
		source string
		cur    any // what was in use
	}
	var before *prev // the user layer's ui.theme before the first preview
	previews := &themePreviews{}
	o := h.Options()
	restore := func() {
		*previews = themePreviews{}
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
		Preview: func(it registry.PickerItem) ui.Node { return themeSwatches(h, previews, it.ID) },
		Run: func(ctx registry.Ctx, it registry.PickerItem) error {
			restore()
			return h.Run(ctx, "settings.set", registry.Args{"name": "ui.theme", "value": it.ID, "scope": "user"})
		},
	}
}
