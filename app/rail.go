package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/ui/widget"
)

// RailView is the project sidebar down the window's left edge (ui.md 01h): each project's header and, under the open
// one, its panels; then the session's entries. Wide, or narrow as an icon strip.
const RailView = "app.rail"

// SidebarKeyMode is the focused sidebar's keymap mode.
const SidebarKeyMode = "sidebar"

const (
	railW        = 44  // the narrow strip
	sidebarW     = 224 // wide, with projects
	sidebarOneW  = 150 // wide, for one project or a directory
	sidebarEntry = 23
)

var (
	tRailBg       = theme.Declare("rail.bg", "bg.surface")
	tRailIcon     = theme.Declare("rail.icon", "fg.faint")
	tRailOn       = theme.Declare("rail.icon.active", "accent")
	tRailOnBg     = theme.DeclareOn("rail.icon.active.bg", "alpha(accent, 8%)", "bg.surface")
	tRailOff      = theme.Declare("rail.icon.unavailable", "fg.ghost")
	tRailBadge    = theme.Declare("rail.badge", "amber")
	tRailBadgeF   = theme.Declare("rail.badge.fg", "ink")
	tSideLabel    = theme.Declare("sidebar.entry.fg", "fg.muted")
	tSideLabelOn  = theme.Declare("sidebar.entry.active.fg", "fg")
	tSideHead     = theme.Declare("sidebar.heading", "fg.faint")
	tSideOpen     = theme.DeclareOn("sidebar.project.open.bg", "alpha(amber, 7%)", "bg.surface")
	tSideName     = theme.Declare("sidebar.project.fg", "fg")
	tSideFolded   = theme.Declare("sidebar.project.folded.fg", "fg.muted")
	tSideAhead    = theme.Declare("sidebar.project.ahead", "green")
	tSideChanged  = theme.Declare("sidebar.project.changed", "amber")
	tSideKey      = theme.Declare("sidebar.key", "fg.faint")
	tSideKeyEdge  = theme.Declare("sidebar.key.border", "line.strong")
	tSideDivider  = theme.Declare("sidebar.divider", "line.strong")
	tSideNote     = theme.Declare("sidebar.entry.note", "fg.faint")
	tSideInitBg   = theme.DeclareOn("sidebar.initial.bg", "alpha(fg, 6%)", "bg.surface")
	tSideToggleBg = theme.Declare("sidebar.toggle.bg", "bg.raised")
)

// rail is the project sidebar, its built-in entries, and the commands that switch projects and panels.
type rail struct {
	disposer
	sh     *shell
	h      ext.Host
	sel    int
	form   string // the window's own: "wide" or "narrow"; "" follows sidebar.form
	writer stateWriter
}

func (r *rail) Manifest() ext.Manifest { return ext.Manifest{ID: "rail"} }

// sidebarState is what the window keeps of the sidebar across restarts.
type sidebarState struct {
	Form string `json:"form,omitempty"`
}

// projectPanels are the built-in project entries: each project's panels in the left dock, with their keys.
var projectPanels = []struct {
	name, icon, title, view, keys string
	order, size                   int
}{
	{"explorer", "files", "Explorer", ExplorerView, "<D-E>", 10, 300},
	{"search", "search", "Search", SearchView, "<D-F>", 20, 380},
	{"git", "git", "Git", "git", "<D-G>", 30, 0},
	{"pull-request", "pullrequest", "Pull request", "pull-request", "", 40, 0},
	{"tests", "tests", "Tests", TestsView, "<D-T>", 50, 0},
}

func (r *rail) Activate(_ context.Context, h ext.Host) error {
	r.h = h
	if path := r.sh.dirState("sidebar"); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			var st sidebarState
			if json.Unmarshal(b, &st) == nil {
				r.form = st.Form
			}
		}
	}
	r.add(h.Option(registry.OptionSpec{Name: "sidebar.form", Type: registry.Enum, Values: []string{"auto", "wide", "narrow"}, Default: "auto",
		Scopes: registry.ScopeUser, Help: "How a window's project sidebar starts: wide, narrow (an icon strip), or auto, wide with two or more projects. «/» switch a window's, which it remembers."}))
	remote := func(project string) bool {
		return r.sh.git.tree(projectsHere(h).dir(project)).remote != ""
	}
	for _, p := range projectPanels {
		e := registry.RailEntry{Name: p.name, Icon: p.icon, Title: p.title, Command: "view.toggle", Args: viewArgs(p.view, p.title, p.size),
			Scope: registry.ScopeProjectEntry, Order: p.order}
		switch p.name {
		case "pull-request":
			e.Shown = remote
		case "git":
			e.Note = func(project string) string {
				if n := uncommittedCount(r.sh.git.tree(projectsHere(h).dir(project)).state()); n > 0 {
					return fmt.Sprint(n)
				}
				return ""
			}
		case "tests":
			e.Note = func(string) string { return r.sh.tests.counts() }
		}
		r.add(h.Contribute(e))
		if p.keys != "" {
			r.add(h.Keymap(registry.Keymap{Mode: "global", Keys: p.keys, Command: "project.panel", Args: registry.Args{"name": p.name}, Desc: p.title}))
		}
	}
	for _, s := range []struct {
		name, icon, title, view, keys, place string
		order                                int
	}{
		{"requests", "requests", "Requests", RequestsView, "<D-R>", "dock", 40},
		{"debug", "debug", "Debug", "debug", "<D-D>", "dock", 50},
		{"ship", "ship", "Ship", "ship", "", "tab", 60},
	} {
		args := registry.Args{"id": s.view, "place": s.place, "edge": "left", "title": s.title}
		switch {
		case s.place == "tab":
			args = registry.Args{"id": s.view, "place": "tab", "title": s.title}
		case s.name == "debug":
			args = debugViewArgs()
		}
		e := registry.RailEntry{Name: s.name, Icon: s.icon, Title: s.title, Command: "view.toggle", Args: args, Order: s.order}
		if s.name == "requests" {
			e.Badge = func() int {
				n := 0
				if se, ok := h.Session(h.State().Focus); ok {
					for _, t := range se.Threads() {
						if t.Open() {
							n++
						}
					}
				}
				return n
			}
		}
		if s.name == "ship" {
			// Ship is the session's whole page: there's something to ship once a project has a remote.
			e.Shown = func(string) bool {
				return slices.ContainsFunc(projectsHere(h).projects, func(p core.Project) bool { return remote(p.Name) })
			}
		}
		r.add(h.Contribute(e))
		if s.keys != "" {
			r.add(h.Keymap(registry.Keymap{Mode: "global", Keys: s.keys, Command: "view.toggle", Args: args, Desc: s.title}))
		}
	}
	r.add(h.Contribute(registry.RailEntry{Name: "settings", Icon: "settings", Title: "Settings", Command: "settings.open", Scope: registry.ScopeGlobalEntry,
		Order: 90, Bottom: true}))
	for n := 1; n <= 9; n++ {
		r.add(h.Keymap(registry.Keymap{Mode: "global", Keys: fmt.Sprintf("<C-%d>", n), Command: "project.open", Args: registry.Args{"index": n},
			Desc: fmt.Sprintf("Project %d", n)}))
	}
	for _, c := range []registry.Command{
		{ID: "project.open", Title: "Open a project of the session: {project}, or the {index}th; it reopens its last panel",
			Available: func(a registry.Args) bool { _, ok := r.target(a); return ok },
			Run: func(ctx registry.Ctx, a registry.Args) error {
				p, ok := r.target(a)
				if !ok {
					return fmt.Errorf("no project %v", cmpOr(a.String("project"), fmt.Sprint(a["index"])))
				}
				return r.open(ctx, p)
			}},
		{ID: "project.panel", Title: "Show or hide a panel of the open project, or of {project}: {name}, a project entry's",
			Available: func(a registry.Args) bool {
				e, ok := r.entry(a.String("name"))
				return ok && h.Available(e.Command, e.ArgsFor(r.scopeOf(a)))
			},
			Run: func(ctx registry.Ctx, a registry.Args) error {
				e, ok := r.entry(a.String("name"))
				if !ok {
					return fmt.Errorf("no project entry %q", a.String("name"))
				}
				return r.run(ctx, e, r.scopeOf(a))
			}},
		{ID: "sidebar.form", Title: "Make this window's sidebar {form}: wide or narrow; with neither, switch",
			Run: func(_ registry.Ctx, a registry.Args) error {
				f := a.String("form")
				if f == "" {
					f = map[string]string{"wide": "narrow", "narrow": "wide"}[r.formNow(projectsHere(h))]
				}
				if f != "wide" && f != "narrow" {
					return fmt.Errorf("a sidebar is wide or narrow, not %q", f)
				}
				r.form = f
				if path := r.sh.dirState("sidebar"); path != "" {
					b, _ := json.Marshal(sidebarState{Form: f})
					r.writer.write(path, b)
				}
				return nil
			}},
	} {
		r.add(h.Command(c))
	}
	r.add(h.View(RailView, registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "rail", Fn: func(c *ui.Ctx) { r.build(c) }}
	})))
	return nil
}

func (r *rail) Deactivate(ctx context.Context) error {
	r.writer.flush()
	return r.disposer.Deactivate(ctx)
}

// target is the project a project.open names: by name, or by its place (1-based).
func (r *rail) target(a registry.Args) (core.Project, bool) {
	w := projectsHere(r.h)
	if name := a.String("project"); name != "" {
		return w.project(name)
	}
	if i := a.Int("index"); i >= 1 && i <= len(w.projects) {
		return w.projects[i-1], true
	}
	return core.Project{}, false
}

// scopeOf is the project a command's {project} names, else the one a panel opened now is for.
func (r *rail) scopeOf(a registry.Args) string {
	if p := a.String("project"); p != "" {
		return p
	}
	return projectsHere(r.h).scope()
}

func (r *rail) entry(name string) (registry.RailEntry, bool) {
	for _, e := range registry.ProjectEntries(r.h) {
		if e.Name == name {
			return e, true
		}
	}
	return registry.RailEntry{}, false
}

// active says an entry's panel is showing for project: its Active, or for one that shows a view, that view.
func active(h ext.Host, e registry.RailEntry, project string) bool {
	if e.Active != nil {
		on := false
		registry.Call(func() error { on = e.Active(project); return nil })
		return on
	}
	if id := e.Args.String("id"); id != "" && strings.HasPrefix(e.Command, "view.") {
		return showing(h, id, project)
	}
	return false
}

// run runs an entry for project, and remembers a project's panel once it's showing.
func (r *rail) run(ctx registry.Ctx, e registry.RailEntry, project string) error {
	if err := r.h.Run(ctx, e.Command, e.ArgsFor(project)); err != nil {
		return err
	}
	w := projectsHere(r.h)
	if e.Scope == registry.ScopeProjectEntry && w.session != nil && project != "" && active(r.h, e, project) {
		return r.sh.cfg.Store.Apply(core.ProjectPanelOpened{ID: w.session.ID, Project: project, Panel: e.Name})
	}
	return nil
}

// open makes p the open project: the docked panels of the others go, and p's last panel (or the one showing for the
// project it replaces) comes back.
func (r *rail) open(ctx registry.Ctx, p core.Project) error {
	w := projectsHere(r.h)
	if w.session == nil {
		return nil
	}
	prev := w.open
	if err := r.sh.cfg.Store.Apply(core.ProjectOpened{ID: w.session.ID, Project: p.Name}); err != nil {
		return err
	}
	panel := w.session.ProjectUI.Panels[p.Name]
	root := r.h.Layout()
	for _, v := range root.All() {
		if v.Project == "" || v.Project == p.Name {
			continue
		}
		if where := root.Find(v); where == nil || where.Placement != layout.Docked {
			continue
		}
		if panel == "" && v.Project == prev && showing(r.h, v.ID, v.Project) {
			for _, e := range registry.ProjectEntries(r.h) {
				if e.Args.String("id") == v.ID {
					panel = e.Name
				}
			}
		}
		root.Close(v)
	}
	if e, ok := r.entry(panel); ok && panel != "" && !active(r.h, e, p.Name) {
		return r.run(ctx, e, p.Name)
	}
	return nil
}

// formNow is the window's form: its own, else the option's, auto being wide with projects to choose between.
func (r *rail) formNow(w here) string {
	if r.form != "" {
		return r.form
	}
	v, _, _ := r.h.Options().Get("sidebar.form", registry.At{})
	if f, _ := v.(string); f == "wide" || f == "narrow" {
		return f
	}
	if w.many() {
		return "wide"
	}
	return "narrow"
}

// canAdd says the user can add a project to the session in front: there's a worker, and it isn't a directory.
func (r *rail) canAdd(w here) bool {
	return w.session != nil && w.session.Kind != core.KindDirect && r.h.Available("project.add", nil)
}

// sideRow is a row of the sidebar.
type sideRow struct {
	kind    int
	project core.Project
	index   int // a project's place, 0-based
	entry   registry.RailEntry
	scope   string // the project an entry runs for; "" for the window's own
}

const (
	rowHeading = iota
	rowProject
	rowEntry
	rowSession
	rowDivider
	rowAdd
	rowGap
)

func (r *rail) rows(w here, wide bool) []sideRow {
	rows := []sideRow{{kind: rowHeading}}
	entries := func(scope string) {
		for _, e := range registry.ProjectEntries(r.h) {
			if e.Is(scope) {
				rows = append(rows, sideRow{kind: rowEntry, entry: e, scope: scope})
			}
		}
	}
	if w.many() {
		for i, p := range w.projects {
			rows = append(rows, sideRow{kind: rowProject, project: p, index: i})
			if p.Name == w.open {
				entries(p.Name)
			}
		}
	} else {
		entries("")
	}
	if !wide && r.canAdd(w) {
		rows = append(rows, sideRow{kind: rowAdd})
	}
	rows = append(rows, sideRow{kind: rowDivider})
	bottom := false
	for _, e := range registry.SessionEntries(r.h) {
		if !e.Is("") {
			continue
		}
		if e.Bottom && !bottom && !wide {
			rows = append(rows, sideRow{kind: rowGap})
		}
		bottom = bottom || e.Bottom
		rows = append(rows, sideRow{kind: rowSession, entry: e})
	}
	// Every session's pages, below a second divider; in the narrow form, pinned to the foot.
	var global []sideRow
	for _, e := range registry.GlobalEntries(r.h) {
		if e.Is("") {
			global = append(global, sideRow{kind: rowSession, entry: e})
		}
	}
	if len(global) > 0 {
		sep := rowDivider
		if !wide {
			sep = rowGap
		}
		rows = append(append(rows, sideRow{kind: sep}), global...)
	}
	return rows
}

func (r *rail) layered(z int) bool {
	u := r.h.UI()
	return u != nil && u.Layered(z)
}

func (r *rail) build(c *ui.Ctx) {
	// While the start view is what the window shows (no project, nothing open, no page over it), the sidebar has
	// nothing to offer.
	if r.h.Here().Project == "" && len(r.h.Layout().Shown()) == 0 && !r.layered(ui.LayerPage) {
		return
	}
	w := projectsHere(r.h)
	th := c.Theme()
	wide := r.formNow(w) == "wide"
	width := float32(railW)
	if wide {
		width = sidebarOneW
		if w.many() {
			width = sidebarW
		}
	}
	rows := r.rows(w, wide)
	static := func(i int) bool {
		k := rows[i].kind
		return k == rowHeading || k == rowDivider || k == rowAdd || k == rowGap
	}
	c.Row("rail", ui.Style{W: ui.Px(width), H: ui.Flex(1), Fill: th.C(tRailBg), Role: ui.RoleToolbar, Label: "Views"}, func(c *ui.Ctx) {
		c.Column("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true}, func(c *ui.Ctx) {
			res := widget.Rows(c, "rows", len(rows), &r.sel, widget.RowsOpts{Help: "shell/rail", Label: "Projects", KeyMode: SidebarKeyMode,
				Height: ui.Flex(1), Static: static, Grow: func(i int) bool { return rows[i].kind == rowGap },
				Disabled: func(i int) bool { return r.unavailable(rows[i]) },
				RowLabel: func(i int) string { return r.label(rows[i]) },
				Tooltip:  func(i int) string { return r.tooltip(w, rows[i], wide) }},
				func(c *ui.Ctx, i int, selected bool) {
					if wide {
						r.wideRow(c, w, rows[i])
					} else {
						r.narrowRow(c, w, rows[i])
					}
				})
			if res.Activated >= 0 {
				r.activate(w, rows[res.Activated])
			}
		})
		c.Box("edge", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
	})
}

func (r *rail) unavailable(row sideRow) bool {
	if row.kind != rowEntry && row.kind != rowSession {
		return false
	}
	return !r.h.Available(row.entry.Command, row.entry.ArgsFor(row.scope))
}

func (r *rail) label(row sideRow) string {
	switch row.kind {
	case rowProject:
		return row.project.Name
	case rowEntry, rowSession:
		return row.entry.Title
	}
	return ""
}

func (r *rail) tooltip(w here, row sideRow, wide bool) string {
	switch row.kind {
	case rowProject:
		if wide {
			return ""
		}
		return fmt.Sprintf("%s  ⌃%d · %s", row.project.Name, row.index+1, strings.Join(r.stateLine(w, row.project).texts(), " · "))
	case rowEntry, rowSession:
		tip := row.entry.Title
		args := row.entry.Args
		if row.kind == rowEntry {
			args = registry.Args{"name": row.entry.Name}
		}
		cmd := row.entry.Command
		if row.kind == rowEntry {
			cmd = "project.panel"
		}
		if k := boundKey(r.h, cmd, args); k != "" {
			tip += "  " + k
		}
		if r.unavailable(row) {
			tip += " (not available yet)"
		}
		if row.entry.Scope == registry.ScopeGlobalEntry && !wide {
			n := noteOf(row)
			if n != "" {
				tip += " · " + n
			}
		}
		return tip
	}
	return ""
}

// noteOf is an entry's note for the row's project: a live figure.
func noteOf(row sideRow) string {
	note := ""
	if row.entry.Note != nil {
		registry.Call(func() error { note = row.entry.Note(row.scope); return nil })
	}
	return note
}

func (r *rail) activate(w here, row sideRow) {
	ctx := context.Background()
	var err error
	switch row.kind {
	case rowProject:
		err = r.open(ctx, row.project)
	case rowEntry, rowSession:
		if r.unavailable(row) {
			return
		}
		err = r.run(ctx, row.entry, row.scope)
	}
	if err != nil {
		r.h.Log().Error("sidebar", "row", r.label(row), "err", err)
	}
}

// statePart is one piece of a header's state line, in its colour.
type statePart struct {
	text string
	tone render.Color
	dot  bool
}

type stateLine []statePart

func (l stateLine) texts() []string {
	var out []string
	for _, p := range l {
		out = append(out, p.text)
	}
	return out
}

// stateLine is a project's header line: branch and ahead/behind, its changes, then what plugins add (a PR, CI).
func (r *rail) stateLine(w here, p core.Project) stateLine {
	th := r.h.CurrentTheme()
	if th == nil {
		return nil
	}
	faint := th.C(theme.FgFaint)
	var out stateLine
	st := r.sh.git.tree(p.Dir).state()
	branch := p.Branch
	if st != nil && st.Branch != "" {
		branch = st.Branch
	}
	if branch != "" {
		out = append(out, statePart{text: branch, tone: faint})
	}
	if st != nil {
		if st.Ahead > 0 {
			out = append(out, statePart{text: fmt.Sprintf("↑%d", st.Ahead), tone: th.C(tSideAhead)})
		}
		if st.Behind > 0 {
			out = append(out, statePart{text: fmt.Sprintf("↓%d", st.Behind), tone: th.C(tSideAhead)})
		}
		if n := uncommittedCount(st); n > 0 {
			out = append(out, statePart{text: fmt.Sprintf("%d changed", n), tone: th.C(tSideChanged)})
		}
	}
	parts := registry.ProjectStateParts(r.h, p.Name, p.Dir)
	for _, s := range parts {
		out = append(out, statePart{text: s.Text, tone: tokenColour(th, s.Style, faint), dot: s.Dot})
	}
	if st != nil && uncommittedCount(st) == 0 && len(parts) == 0 {
		out = append(out, statePart{text: "clean", tone: faint})
	}
	return out
}

// wideRow draws a row of the wide sidebar (42's panels 1, 2, 4 and 5).
func (r *rail) wideRow(c *ui.Ctx, w here, row sideRow) {
	th := c.Theme()
	switch row.kind {
	case rowHeading:
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(28), Pad: ui.Insets{L: 12, R: 6}, Gap: 4, Cross: ui.Center}, func(c *ui.Ctx) {
			label := "PROJECTS"
			if !w.many() {
				label = strings.ToUpper(w.open)
				if w.session == nil || w.session.Kind == core.KindDirect {
					label = tildePath(w.dir(""))
				}
			}
			c.Row("label", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
				c.Text("t", label, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.SemiBold, Tracking: .12},
					Color: th.C(tSideHead)})
			})
			r.addButton(c, w)
			if widget.IconButton(c, "form", "«", widget.IconButtonOpts{Label: "Narrow the sidebar", Help: "shell/rail", Size: 20}) {
				r.h.Run(context.Background(), "sidebar.form", registry.Args{"form": "narrow"})
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Box("pad", ui.Style{H: ui.Px(4)}, nil)
	case rowProject:
		r.header(c, w, row)
	case rowEntry, rowSession:
		r.wideEntry(c, row)
	case rowDivider:
		c.Box("div", ui.Style{W: ui.Flex(1), H: ui.Px(11), Pad: ui.Insets{L: 10, R: 10, T: 5, B: 5}}, func(c *ui.Ctx) {
			c.Box("line", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tSideDivider)}, nil)
		})
	}
}

// addButton is + in the sidebar's heading: add a project to the session.
func (r *rail) addButton(c *ui.Ctx, w here) {
	if r.canAdd(w) && widget.IconButton(c, "add", "+", widget.IconButtonOpts{Label: "Add a project", Help: "shell/rail", Size: 20}) {
		if err := r.h.Run(context.Background(), "project.add", nil); err != nil {
			r.h.Log().Error("sidebar", "add", err)
		}
	}
}

// header is a project's two lines: twisty, colour, name and ⌃n; then its state.
func (r *rail) header(c *ui.Ctx, w here, row sideRow) {
	th := c.Theme()
	open := row.project.Name == w.open
	fill := render.Color{}
	if open {
		fill = th.C(tSideOpen)
	}
	c.Column("project", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 10, R: 8, T: 5, B: 5}, Gap: 2, Fill: fill}, func(c *ui.Ctx) {
		c.Row("l1", ui.Style{W: ui.Flex(1), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
			twisty, name := "▸", th.C(tSideFolded)
			if open {
				twisty, name = "▾", th.C(tSideName)
			}
			c.Box("tw", ui.Style{W: ui.Px(9)}, func(c *ui.Ctx) { c.Text("t", twisty, mono(11, typeset.Normal, th.C(tSideHead))) })
			c.Box("sq", ui.Style{W: ui.Px(8), H: ui.Px(8), Radius: 2, Fill: w.colour(th, row.project.Name)}, nil)
			c.Text("n", row.project.Name, mono(11, typeset.SemiBold, name))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if row.index < 9 {
				c.Box("key", ui.Style{Pad: ui.PadXY(4, 1), Radius: 3, Stroke: th.C(tSideKeyEdge), Border: 1}, func(c *ui.Ctx) {
					c.Text("k", fmt.Sprintf("⌃%d", row.index+1), mono(9, typeset.Normal, th.C(tSideKey)))
				})
			}
		})
		line := r.stateLine(w, row.project)
		c.Row("l2", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 15}, Gap: 4, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
			for i, p := range line {
				if i > 0 && !p.dot && !strings.HasPrefix(p.text, "↑") && !strings.HasPrefix(p.text, "↓") {
					c.Text(fmt.Sprint("sep", i), "·", mono(10, typeset.Normal, th.C(tSideHead)))
				}
				if p.dot {
					c.Box(fmt.Sprint("dot", i), ui.Style{W: ui.Px(6), H: ui.Px(6), Radius: 3, Fill: p.tone}, nil)
				}
				c.Text(i, p.text, mono(10, typeset.Normal, p.tone))
			}
		})
	})
}

// entryNote is the right of an entry: its badge as a pill, else its note.
func (r *rail) entryNote(c *ui.Ctx, row sideRow, size float32) {
	th := c.Theme()
	if n := badge(row.entry); n > 0 {
		c.Box("badge", ui.Style{H: ui.Px(14), Pad: ui.PadXY(4, 0), Radius: 7, Fill: th.C(tRailBadge), Cross: ui.Center, Main: ui.Center}, func(c *ui.Ctx) {
			c.Text("n", fmt.Sprint(n), mono(size, typeset.Bold, th.C(tRailBadgeF)))
		})
		return
	}
	if note := noteOf(row); note != "" {
		c.Text("note", note, mono(10, typeset.Normal, th.C(tSideNote)))
	}
}

func (r *rail) wideEntry(c *ui.Ctx, row sideRow) {
	th := c.Theme()
	off := r.unavailable(row)
	on := !off && active(r.h, row.entry, row.scope)
	icol, lcol, fill, edge := th.C(tRailIcon), th.C(tSideLabel), render.Color{}, render.Color{}
	switch {
	case on:
		icol, lcol, fill, edge = th.C(tRailOn), th.C(tSideLabelOn), th.C(tRailOnBg), th.C(tRailOn)
	case off:
		icol, lcol = th.C(tRailOff), th.C(tRailOff)
	}
	indent := float32(12)
	if row.kind == rowEntry && row.scope != "" {
		indent = 33
	}
	c.Row("entry", ui.Style{W: ui.Flex(1), H: ui.Px(sidebarEntry), Fill: fill, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
		c.Row("in", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Insets{L: indent - 2, R: 10}, Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			icon(c, "icon", row.entry.Icon, 13, icol)
			c.Text("t", row.entry.Title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: lcol})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			r.entryNote(c, row, 9)
		})
	})
}

// narrowRow draws a row of the icon strip (42's panel 3).
func (r *rail) narrowRow(c *ui.Ctx, w here, row sideRow) {
	th := c.Theme()
	cell := func(key string, h float32, body func(c *ui.Ctx)) {
		c.Row(key, ui.Style{W: ui.Flex(1), H: ui.Px(h), Main: ui.Center, Cross: ui.Center}, body)
	}
	switch row.kind {
	case rowHeading:
		cell("head", 30, func(c *ui.Ctx) {
			if widget.IconButton(c, "form", "»", widget.IconButtonOpts{Label: "Widen the sidebar", Help: "shell/rail", Size: 22}) {
				r.h.Run(context.Background(), "sidebar.form", registry.Args{"form": "wide"})
			}
		})
	case rowAdd:
		cell("add", 28, func(c *ui.Ctx) { r.addButton(c, w) })
	case rowProject:
		col := w.colour(th, row.project.Name)
		open := row.project.Name == w.open
		cell("project", 28, func(c *ui.Ctx) {
			st := ui.Style{W: ui.Px(30), H: ui.Px(24), Radius: 6, Fill: th.C(tSideInitBg), Main: ui.Center, Cross: ui.Center}
			if open {
				st.Stroke, st.Border = col, 1
			}
			c.Box("ini", st, func(c *ui.Ctx) {
				c.Text("t", projectInitials(row.project.Name), mono(10, typeset.Bold, col))
				for _, p := range r.stateLine(w, row.project) {
					if p.dot {
						c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: p.tone,
							Float: &ui.Float{At: render.Point{X: 1}, Self: render.Point{X: 0.5, Y: 0.5}, Offset: render.Point{X: -1, Y: 1}}}, nil)
						break
					}
				}
			})
		})
	case rowEntry, rowSession:
		off := r.unavailable(row)
		on := !off && active(r.h, row.entry, row.scope)
		col, fill, edge := th.C(tRailIcon), render.Color{}, render.Color{}
		switch {
		case on:
			col, fill, edge = th.C(tRailOn), th.C(tRailOnBg), th.C(tRailOn)
		case off:
			col = th.C(tRailOff)
		}
		cell("entry", 34, func(c *ui.Ctx) {
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Px(18), Radius: 1, Fill: edge}, nil)
			c.Box("gap", ui.Style{W: ui.Px(4)}, nil)
			c.Box("ic", ui.Style{W: ui.Px(32), H: ui.Px(32), Radius: 7, Fill: fill, Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
				icon(c, "icon", row.entry.Icon, 16, col)
				if n := badge(row.entry); n > 0 {
					c.Box("badge", ui.Style{H: ui.Px(13), Pad: ui.PadXY(3, 0), Radius: 6.5, Fill: th.C(tRailBadge), Cross: ui.Center,
						Float: &ui.Float{At: render.Point{X: 1}, Self: render.Point{X: 1}, Offset: render.Point{X: -2, Y: 3}}}, func(c *ui.Ctx) {
						c.Text("n", fmt.Sprint(n), mono(8.5, typeset.Bold, th.C(tRailBadgeF)))
					})
				}
			})
			c.Box("gap2", ui.Style{W: ui.Px(6)}, nil)
		})
	case rowDivider:
		cell("div", 9, func(c *ui.Ctx) { c.Box("line", ui.Style{W: ui.Px(24), H: ui.Px(1), Fill: th.C(tSideDivider)}, nil) })
	case rowGap:
		c.Box("gap", ui.Style{H: ui.Flex(1)}, nil)
	}
}

// projectInitials are a project's two letters for the narrow sidebar: "admin-ui" is AD.
func projectInitials(name string) string {
	var rs []rune
	for _, r := range strings.ToUpper(name) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			rs = append(rs, r)
		}
		if len(rs) == 2 {
			break
		}
	}
	return string(rs)
}

func badge(e registry.RailEntry) int {
	if e.Badge == nil {
		return 0
	}
	n := 0
	registry.Call(func() error { n = e.Badge(); return nil })
	return n
}

// boundKey is the key bound to command with args right now, as the keyboard shows it ("⌘⇧E"), or "".
func boundKey(h ext.Host, command string, args registry.Args) string {
	for _, k := range h.Keymaps() {
		if k.Command == command && (k.Mode == "global" || k.Mode == "n") && sameArgs(k.Args, args) {
			return keyLabel(k.Keys)
		}
	}
	return ""
}

func sameArgs(a, b registry.Args) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if fmt.Sprint(b[k]) != fmt.Sprint(v) {
			return false
		}
	}
	return true
}
