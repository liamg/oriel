package shell

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry/uireg"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/ui/layout/panes"
	"github.com/liamg/oriel/ui/widget"
)

// layoutPlugin is the layout every view lives in, and the commands and ⌃W keys that move views
// around it. view.open is how anything, built-in or plugin, puts a view on screen.
type layoutPlugin struct {
	kit.Disposer
	sh    *shell
	h     plugin.Host
	root  *layout.Root
	panes panes.Panes
	rects map[*layout.Node]render.Rect
	snaps map[string]layout.Snapshot
	menu  bool
	asked bool // the menu asked for focus

	restored bool
	kept     []byte                     // the layout as last written
	live     map[string]layout.Snapshot // each directory left, by its state file: its layout, its views still open
	keptAt   map[string][]byte          // and its layout as last written
	saved    []byte                     // the layout kept for the directory the window is on, as its file has it
	state    *kit.StateFile             // that file
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
	l.h, l.root, l.snaps = h, layout.New(), map[string]layout.Snapshot{}
	l.live, l.keptAt = map[string]layout.Snapshot{}, map[string][]byte{}
	path := func() string { return "" }
	if l.sh != nil {
		path = func() string { return l.sh.dirState("layout") }
	}
	l.state = kit.NewStateFile(h, kit.StateFileOpts{Name: "layout", Path: path, Load: func(b []byte) { l.saved = b }})
	l.Add(kit.OnDispose(l.state.Close))
	// A view put in front is what the user asked to see: the page over the layout gives way to it.
	l.root.OnShow(func(layout.ViewRef) { closePlaces(h) })
	l.Add(h.DeclareEvent(registry.EventInfo{Name: registry.EventName[kit.DirChanged](), Fired: true, Payload: kit.DirChanged{},
		Doc: "The window moved onto another directory, as a session in another one came to the front; plugins live on across it, and one that keeps something per directory swaps it here. The new directory's layout comes back after its hooks, in place of any view one opens. { dir, was, session }"}))
	h.Do(func() { // once every plugin has registered its views, and what they hold it for is read
		if l.sh != nil {
			l.sh.arriveWhenReady()
		} else {
			l.arrive()
		}
	})
	l.panes = l.newPanes(h)
	for _, c := range l.commands(h) {
		l.Add(h.Command(c))
	}
	for _, k := range layoutKeys() {
		l.Add(h.Keymap(k))
	}
	l.Add(h.View(kit.RootView, l.view(h)))
	return nil
}

// arrive lays out the directory the window is on: the layout saved for it, then, with nothing laid out, the session
// it's on started the way its kind opens if it never has, then what was asked to run once the window got there.
func (l *layoutPlugin) arrive() {
	h := l.h
	if !l.back(h) {
		l.restore(h)
	}
	l.restored = true
	f := h.State().Focus
	if l.sh != nil && l.sh.openedFor != "" {
		f = l.sh.openedFor // the session it moved for, though another may be in front by now
	}
	if f != "" && slices.ContainsFunc(h.Commands(), func(c registry.Command) bool { return c.ID == "session.opens" }) {
		kit.RunCalls(h, []kit.Call{{Command: "session.opens", Args: registry.Args{"id": string(f)}}})
	}
	if l.sh != nil && l.sh.afterOpen != nil && l.sh.afterOpen.session == f {
		calls := l.sh.afterOpen.calls
		l.sh.afterOpen = nil
		kit.RunCalls(h, calls)
	}
}

// leave saves the layout for the directory the window is leaving, keeps it as it is for the window's return, and
// empties it for the next one's; the pages over it close, as the layout under them goes.
func (l *layoutPlugin) leave() {
	closePlaces(l.h)
	l.keep(l.h) // to the directory's file it's leaving: the state file follows the window after
	if p := l.state.Path(); l.restored && p != "" {
		l.live[p], l.keptAt[p] = l.root.Save(), l.kept
	}
	l.restored, l.kept = false, nil
	if err := l.root.Load(layout.New().Save()); err != nil {
		l.h.Log().Warn("emptying the layout", "err", err)
	}
}

// newPanes is how the layout draws: what fills the empty main area, and each tab's title, mark, lead colour and tip.
func (l *layoutPlugin) newPanes(h plugin.Host) panes.Panes {
	var panes panes.Panes
	panes.Empty = func(c *ui.Ctx) {
		id := StartView
		switch {
		case l.sh == nil:
		case l.sh.heading.Load() != nil:
			id = OpeningView
		case l.sh.cfg.WorkDir != "":
			id = OverviewView
		}
		if v, ok := h.ViewByID(id); ok {
			c.Column(id, ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) { c.Node(v.Build(uireg.ViewCtx{})) })
		}
	}
	panes.Title = func(v layout.ViewRef) string {
		if view, ok := h.ViewByID(v.ID); ok {
			if t, ok := view.(uireg.Titled); ok {
				return t.Title(v.Instance)
			}
		}
		return ""
	}
	panes.Mark = func(v layout.ViewRef) (string, render.Color) {
		view, ok := h.ViewByID(v.ID)
		if !ok {
			return "", render.Color{}
		}
		m, ok := uireg.As[uireg.Marked](view)
		if !ok {
			return "", render.Color{}
		}
		text, style := m.Mark(v.Instance)
		if text == "" {
			return "", render.Color{}
		}
		return text, kit.TokenColour(h.CurrentTheme(), style, h.CurrentTheme().C(theme.FgFaint))
	}
	panes.Lead = func(v layout.ViewRef) (render.Color, bool) {
		w := kit.ProjectsHere(h)
		p, ok := kit.ViewProject(h, w, v)
		if !ok || !w.Many() || h.CurrentTheme() == nil {
			return render.Color{}, false
		}
		return w.Colour(h.CurrentTheme(), p.Name), true
	}
	panes.Tip = func(v layout.ViewRef) string {
		w := kit.ProjectsHere(h)
		p, ok := kit.ViewProject(h, w, v)
		if !ok || !w.Many() {
			return ""
		}
		if file := kit.ViewFile(h, v); file != "" {
			if rel, err := filepath.Rel(p.Dir, file); err == nil {
				return p.Name + " · " + filepath.ToSlash(rel)
			}
		}
		return p.Name
	}
	return panes
}

// commands are view.open and the layout's own.
func (l *layoutPlugin) commands(h plugin.Host) []registry.Command {
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
		_, held := h.Holder(registry.KindView, id)
		return ok || held
	}
	return []registry.Command{
		{ID: "view.open", Title: "Open a view: {id, instance, place, edge, side, size, beside, title, project}; alone, in a dock, closes what was there; fixed, it can't be dragged away", Available: viewRegistered, Run: func(_ registry.Ctx, a registry.Args) error {
			id := str(a, "id")
			if _, ok := viewFor(h, id); !ok {
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
		{ID: "view.close", Title: "Close a view: the focused one, or {id, instance, project}; with {docked}, only one outside the main area or a page on Home", Run: func(_ registry.Ctx, a registry.Args) error {
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
			if w := l.root.Find(v); a["docked"] == true && (w == nil || w.Placement == layout.Tab) && !l.onHome(h) {
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
			l.snaps[str(a, "name")] = l.root.Save()
			return nil
		}},
		{ID: "layout.restore", Title: "Restore a saved layout", Run: func(_ registry.Ctx, a registry.Args) error {
			s, ok := l.snaps[str(a, "name")]
			if !ok {
				return fmt.Errorf("no layout %q", str(a, "name"))
			}
			return l.root.Load(s)
		}},
		{ID: "layout.menu", Title: "Move the focused view…", Run: func(registry.Ctx, registry.Args) error {
			l.menu, l.asked = !l.menu, false
			return nil
		}},
	}
}

// layoutKeys are ⌃W's keys, everywhere, and q and Escape on a view with none of its own.
func layoutKeys() []registry.Keymap {
	var out []registry.Keymap
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
		{Keys: "<D-w>", Command: "view.close", Desc: "Close the tab in front"},
	} {
		k.Mode = "global"
		out = append(out, k)
	}
	for _, k := range []registry.Keymap{
		{Keys: "q", Command: "view.close", Desc: "Close"},
		{Keys: "<C-w>c", Command: "view.close", Desc: "Close"},
		{Keys: "<C-w>q", Command: "view.close", Desc: "Close"},
		{Keys: "<Esc>", Command: "view.close", Args: registry.Args{"docked": true}, Desc: "Close a docked view"},
	} {
		k.Mode = ViewKeyMode
		out = append(out, k)
	}
	return out
}

// view is the layout's root view: the panes, and ⌃W M's menu over them.
func (l *layoutPlugin) view(h plugin.Host) uireg.View {
	return uireg.ViewFunc(func(vc uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "layout", Fn: func(c *ui.Ctx) {
			l.rects = l.panes.RectsIn(c)
			panes.View(c, "panes", l.root, &l.panes, func(c *ui.Ctx, v layout.ViewRef, focused bool) {
				view, ok := h.ViewByID(v.ID)
				if !ok {
					c.Text("missing", "no view "+v.ID, ui.TextStyle{})
					return
				}
				pad := ui.Pad(10)
				if _, edge := uireg.As[uireg.EdgeToEdge](view); edge {
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
	})
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
		items[i] = widget.MenuItem{Label: p.label, Shortcut: p.keys, Help: panes.HelpPane}
	}
	at := widget.AreaFloat(c, widget.AreaMain, render.Point{X: 1}, render.Point{}, render.Point{X: -340, Y: 2})
	menu := ui.Child(c.Next("move"), "menu")
	if !l.asked {
		l.asked = true
		c.RequestFocus(menu)
	}
	c.Box("move", ui.Style{Z: ui.LayerPopup, Float: at}, func(c *ui.Ctx) {
		chosen, closed := widget.Menu(c, "menu", items, widget.MenuOpts{Width: 320, Help: panes.HelpPane})
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

// viewFor is the view registered as id, provided first by the placeholder that stands for it, if one does.
func viewFor(h plugin.Host, id string) (uireg.View, bool) {
	if v, ok := h.ViewByID(id); ok {
		return v, true
	}
	if provide, ok := h.Holder(registry.KindView, id); ok {
		if err := provide(); err != nil {
			h.Log().Warn("loading a view", "view", id, "err", err)
			return nil, false
		}
	}
	return h.ViewByID(id)
}

// onHome says the window shows Home, where a view opens as a page over the start view (home).
func (l *layoutPlugin) onHome(h plugin.Host) bool {
	return l.sh != nil && !sessionRunning(frameWindow(h.State(), l.sh.cfg.WorkDir))
}
