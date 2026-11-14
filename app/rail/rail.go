// Package rail is the project sidebar: the window's projects and the panels each opens, with their entries.
package rail

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/ui/widget"
)

// RailView is the project sidebar (kit.RailView).
const RailView = kit.RailView

// SidebarKeyMode is the focused sidebar's keymap mode.
const SidebarKeyMode = "sidebar"

const (
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

// Rail is the project sidebar, its built-in entries, and the commands that switch projects and panels.
type Rail struct {
	kit.Disposer
	// DirState is a state file kept for the directory the window is on now, as it moves.
	DirState func(name string) string
	h        plugin.Host
	sel      int
	form     string         // the window's own: "wide" or "narrow"; "" follows sidebar.form
	state    *kit.StateFile // the form, kept for the window's directory
	counts   []railCount    // the git entries' notes, by the state they were counted in
}

// loadForm takes the form the window's directory's sidebar was left in: none for nil.
func (r *Rail) loadForm(b []byte) {
	r.form = ""
	var st sidebarState
	if b != nil && json.Unmarshal(b, &st) == nil {
		r.form = st.Form
	}
}

type railCount struct {
	st *gitx.State
	n  int
}

// uncommitted is kit.UncommittedCount(st), counted once per state; the states of the last few projects are kept.
func (r *Rail) uncommitted(st *gitx.State) int {
	for _, c := range r.counts {
		if c.st == st {
			return c.n
		}
	}
	c := railCount{st, kit.UncommittedCount(st)}
	r.counts = append(r.counts[max(0, len(r.counts)-15):], c)
	return c.n
}

func (r *Rail) Manifest() plugin.Manifest { return plugin.Manifest{ID: "rail"} }

// sidebarState is what the window keeps of the sidebar across restarts.
type sidebarState struct {
	Form string `json:"form,omitempty"`
}

// projectPanels are the built-in project entries: each project's panels in the left dock, with their keys.
var projectPanels = []struct {
	name, icon, title, view, keys string
	order, size                   int
}{
	{"explorer", "files", "Explorer", kit.ExplorerView, "<D-E>", 10, 300},
	{"search", "search", "Search", kit.SearchView, "<D-F>", 20, 380},
	{"git", "git", "Git", kit.GitView, "<D-G>", 30, 0},
	{"pull-request", "pullrequest", "Pull request", kit.PullRequestView, "", 40, 0},
}

func (r *Rail) Activate(_ context.Context, h plugin.Host) error {
	r.h = h
	r.state = kit.NewStateFile(h, kit.StateFileOpts{Name: "sidebar", Path: func() string { return r.DirState("sidebar") }, Load: r.loadForm})
	r.Add(registry.DisposeFunc(r.state.Close))
	r.Add(kit.OnDirChanged(h, "the sidebar's selection is of its directory", func(kit.DirChanged) { r.sel = 0 }))
	r.Add(h.Option(registry.OptionSpec{Name: "sidebar.form", Type: registry.Enum, Values: []string{"auto", "wide", "narrow"}, Default: "auto",
		Scopes: registry.ScopeUser, Help: "How a window's project sidebar starts: wide, narrow (an icon strip), or auto, wide with two or more projects. «/» switch a window's, which it remembers."}))
	r.entries(h)
	r.Add(h.Contribute(uireg.RailEntry{Name: "settings", Icon: "settings", Title: "Settings", Command: "settings.open", Scope: uireg.ScopeGlobalEntry,
		Order: 90, Bottom: true}))
	for n := 1; n <= 9; n++ {
		r.Add(h.Keymap(registry.Keymap{Mode: "global", Keys: fmt.Sprintf("<C-%d>", n), Command: "project.open", Args: registry.Args{"index": n},
			Desc: fmt.Sprintf("Project %d", n)}))
	}
	for _, c := range r.commands(h) {
		r.Add(h.Command(c))
	}
	r.Add(h.View(RailView, uireg.ViewFunc(func(vc uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "rail", Fn: func(c *ui.Ctx) { r.build(c, vc) }}
	})))
	return nil
}

// entries are the built-in rail entries: each project's panels, Requests and Ship, with their keys.
func (r *Rail) entries(h plugin.Host) {
	remote := func(project string) bool {
		return kit.GitOf(h).Tree(kit.ProjectsHere(h).Dir(project)).Remote() != ""
	}
	for _, p := range projectPanels {
		e := uireg.RailEntry{Name: p.name, Icon: p.icon, Title: p.title, Command: "view.toggle", Args: kit.ViewArgs(p.view, p.title, p.size),
			Scope: uireg.ScopeProjectEntry, Order: p.order}
		switch p.name {
		case "pull-request":
			e.When, e.Shown = core.Ships, remote
		case "git":
			e.When = core.OwnsChanges
			// The session's uncommitted files; a project it only reads has none of its own.
			e.Note = func(project string) string {
				w := kit.ProjectsHere(h)
				p, _ := w.Project(cmp.Or(project, w.Open))
				if p.ReadOnly {
					return ""
				}
				if n := r.uncommitted(kit.GitOf(h).Tree(p.Dir).State()); n > 0 {
					return fmt.Sprint(n)
				}
				return ""
			}
		}
		r.Add(h.Contribute(e))
		if p.keys != "" {
			r.Add(h.Keymap(registry.Keymap{Mode: "global", Keys: p.keys, Command: "project.panel", Args: registry.Args{"name": p.name}, Desc: p.title}))
		}
	}
	for _, s := range []struct {
		name, icon, title, view, keys, place string
		order                                int
	}{
		{"requests", "requests", "Requests", kit.RequestsView, "<D-R>", "dock", 40},
		{"ship", "ship", "Ship", "ship", "", "tab", 60},
	} {
		args := kit.SidebarArgs("view.toggle", registry.Args{"id": s.view, "place": s.place, "edge": "left", "title": s.title})
		if s.place == "tab" {
			args = registry.Args{"id": s.view, "place": "tab", "title": s.title}
		}
		e := uireg.RailEntry{Name: s.name, Icon: s.icon, Title: s.title, Command: "view.toggle", Args: args, Order: s.order}
		if s.name == "requests" {
			e.Badge = func() int {
				n := 0
				if se, ok := h.Session(h.State().Focus); ok {
					for _, t := range se.Threads() {
						if kit.GroupOf(t) == kit.GroupNeedsYou {
							n++
						}
					}
				}
				return n
			}
		}
		if s.name == "ship" {
			// Ship is the session's whole page: there's something to ship once a project has a remote.
			e.When = core.Ships
			e.Shown = func(string) bool {
				return slices.ContainsFunc(kit.ProjectsHere(h).Projects, func(p core.Project) bool { return remote(p.Name) })
			}
		}
		r.Add(h.Contribute(e))
		if s.keys != "" {
			r.Add(h.Keymap(registry.Keymap{Mode: "global", Keys: s.keys, Command: "view.toggle", Args: args, Desc: s.title}))
		}
	}
}

// commands are project.open, project.move, project.fold, project.panel and sidebar.form.
func (r *Rail) commands(h plugin.Host) []registry.Command {
	return []registry.Command{
		{ID: "project.open", Title: "Open a project of the session: {project}, or the {index}th; it reopens its last panel",
			Available: func(a registry.Args) bool { _, ok := r.target(a); return ok },
			Run: func(ctx registry.Ctx, a registry.Args) error {
				p, ok := r.target(a)
				if !ok {
					return fmt.Errorf("no project %v", cmp.Or(a.String("project"), fmt.Sprint(a["index"])))
				}
				return r.open(ctx, p)
			}},
		{ID: "project.move", Title: "Move a project of the session in front, {project}, to the {index}th place in the sidebar",
			Available: func(a registry.Args) bool {
				_, ok := r.target(registry.Args{"project": a.String("project")})
				return ok
			},
			Run: func(_ registry.Ctx, a registry.Args) error {
				w := kit.ProjectsHere(h)
				p, ok := w.Project(a.String("project"))
				if !ok || w.Session == nil {
					return fmt.Errorf("no project %q", a.String("project"))
				}
				if i := a.Int("index"); i < 1 || i > len(w.Projects) {
					return fmt.Errorf("no place %d among %d projects", i, len(w.Projects))
				}
				return h.Apply(core.ProjectMoved{ID: w.Session.ID, Project: p.Name, Index: a.Int("index") - 1})
			}},
		{ID: "project.fold", Title: "Fold the open project's panels away in the sidebar, or unfold them: {folded}; with neither, switch",
			Available: func(registry.Args) bool { w := kit.ProjectsHere(h); return w.Session != nil && w.Many() },
			Run: func(_ registry.Ctx, a registry.Args) error {
				w := kit.ProjectsHere(h)
				if w.Session == nil {
					return errors.New("no session in front")
				}
				folded := !w.Session.ProjectUI.Folded
				if v, ok := a["folded"].(bool); ok {
					folded = v
				}
				return h.Apply(core.ProjectFolded{ID: w.Session.ID, Folded: folded})
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
					f = map[string]string{"wide": "narrow", "narrow": "wide"}[r.formNow(kit.ProjectsHere(h))]
				}
				if f != "wide" && f != "narrow" {
					return fmt.Errorf("a sidebar is wide or narrow, not %q", f)
				}
				r.form = f
				b, _ := json.Marshal(sidebarState{Form: f})
				r.state.Write(b)
				return nil
			}},
	}
}

func (r *Rail) Deactivate(ctx context.Context) error { return r.Disposer.Deactivate(ctx) }

// target is the project a project.open names: by name, or by its place (1-based).
func (r *Rail) target(a registry.Args) (core.Project, bool) {
	w := kit.ProjectsHere(r.h)
	if name := a.String("project"); name != "" {
		return w.Project(name)
	}
	if i := a.Int("index"); i >= 1 && i <= len(w.Projects) {
		return w.Projects[i-1], true
	}
	return core.Project{}, false
}

// scopeOf is the project a command's {project} names, else the one a panel opened now is for.
func (r *Rail) scopeOf(a registry.Args) string {
	if p := a.String("project"); p != "" {
		return p
	}
	return kit.ProjectsHere(r.h).Scope()
}

func (r *Rail) entry(name string) (uireg.RailEntry, bool) {
	for _, e := range uireg.ProjectEntries(r.h) {
		if e.Name == name {
			return e, true
		}
	}
	return uireg.RailEntry{}, false
}

// active says an entry's panel is showing for project: its Active, or for one that shows a view, that view.
func active(h plugin.Host, e uireg.RailEntry, project string) bool {
	if e.Active != nil {
		on := false
		registry.Call(func() error { on = e.Active(project); return nil })
		return on
	}
	if id := e.Args.String("id"); id != "" && strings.HasPrefix(e.Command, "view.") {
		return kit.Showing(h, id, project)
	}
	return false
}

// run runs an entry for project, and remembers a project's panel once it's showing.
func (r *Rail) run(ctx registry.Ctx, e uireg.RailEntry, project string) error {
	if err := r.h.Run(ctx, e.Command, kit.SidebarArgs(e.Command, e.ArgsFor(project))); err != nil {
		return err
	}
	w := kit.ProjectsHere(r.h)
	if e.Scope == uireg.ScopeProjectEntry && w.Session != nil && project != "" && active(r.h, e, project) {
		return r.h.Apply(core.ProjectPanelOpened{ID: w.Session.ID, Project: project, Panel: e.Name})
	}
	return nil
}

// open makes p the open project: the docked panels of the others go, and p's last panel (or the one showing for the
// project it replaces) comes back.
func (r *Rail) open(ctx registry.Ctx, p core.Project) error {
	w := kit.ProjectsHere(r.h)
	if w.Session == nil {
		return nil
	}
	prev := w.Open
	if err := r.h.Apply(core.ProjectOpened{ID: w.Session.ID, Project: p.Name}); err != nil {
		return err
	}
	panel := w.Session.ProjectUI.Panels[p.Name]
	root := r.h.Layout()
	for _, v := range root.All() {
		if v.Project == "" || v.Project == p.Name {
			continue
		}
		if where := root.Find(v); where == nil || where.Placement != layout.Docked {
			continue
		}
		if panel == "" && v.Project == prev && kit.Showing(r.h, v.ID, v.Project) {
			for _, e := range uireg.ProjectEntries(r.h) {
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

// Form is the form the sidebar takes for the projects here: wide, narrow, one, or none.
func (r *Rail) Form() string { return r.formNow(kit.ProjectsHere(r.h)) }

// formNow is the window's form: its own, else the option's, auto being wide with projects to choose between.
func (r *Rail) formNow(w kit.WindowProjects) string {
	if r.form != "" {
		return r.form
	}
	if f, _ := registry.Option[string](r.h.Options(), "sidebar.form", registry.At{}); f == "wide" || f == "narrow" {
		return f
	}
	if w.Many() {
		return "wide"
	}
	return "narrow"
}

// canAdd says the user can add a project to the session in front: there are sessions, and it isn't a directory.
func (r *Rail) canAdd(w kit.WindowProjects) bool {
	return w.Session != nil && registry.Can(r.h, w.Session.Kind, core.AddProjects) && r.h.Available("project.add", nil)
}

// sideRow is a row of the sidebar.
type sideRow struct {
	kind    int
	project core.Project
	index   int // a project's place, 0-based
	entry   uireg.RailEntry
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

func (r *Rail) rows(w kit.WindowProjects, wide bool) []sideRow {
	rows := []sideRow{{kind: rowHeading}}
	var kind core.Kind
	if w.Session != nil {
		kind = w.Session.Kind
	}
	entries := func(scope string) {
		for _, e := range uireg.ProjectEntries(r.h) {
			if e.For(r.h, kind) && e.Listed(scope) {
				rows = append(rows, sideRow{kind: rowEntry, entry: e, scope: scope})
			}
		}
	}
	if w.Many() {
		for i, p := range w.Projects {
			rows = append(rows, sideRow{kind: rowProject, project: p, index: i})
			if p.Name == w.Open && !w.Folded() {
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
	for _, e := range uireg.SessionEntries(r.h) {
		if !e.For(r.h, kind) || !e.Listed("") {
			continue
		}
		if e.Bottom && !bottom {
			rows = append(rows, sideRow{kind: rowGap})
		}
		bottom = bottom || e.Bottom
		rows = append(rows, sideRow{kind: rowSession, entry: e})
	}
	// Every session's pages, pinned to the foot; in the wide form, below a second divider.
	var global []sideRow
	for _, e := range uireg.GlobalEntries(r.h) {
		if e.For(r.h, kind) && e.Listed("") {
			global = append(global, sideRow{kind: rowSession, entry: e})
		}
	}
	if len(global) > 0 {
		if !bottom {
			rows = append(rows, sideRow{kind: rowGap})
		}
		if wide {
			rows = append(rows, sideRow{kind: rowDivider})
		}
		rows = append(rows, global...)
	}
	return rows
}

func (r *Rail) build(c *ui.Ctx, vc uireg.ViewCtx) {
	w := kit.ProjectsIn(r.h.Here().Project, vc.Session)
	th := c.Theme()
	wide := r.formNow(w) == "wide"
	width := float32(kit.RailW)
	if wide {
		width = sidebarOneW
		if w.Many() {
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
				Movable:  func(i int) bool { return rows[i].kind == rowProject },
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
			if m := res.Moved; m != nil {
				from, to := rows[m.From], rows[m.To]
				if err := r.h.Run(context.Background(), "project.move", registry.Args{"project": from.project.Name, "index": to.index + 1}); err != nil {
					r.h.Log().Error("sidebar", "move", from.project.Name, "err", err)
				}
			}
		})
		widget.VRule(c, "edge")
	})
}

func (r *Rail) unavailable(row sideRow) bool {
	if row.kind != rowEntry && row.kind != rowSession {
		return false
	}
	return !r.h.Available(row.entry.Command, row.entry.ArgsFor(row.scope))
}

func (r *Rail) label(row sideRow) string {
	switch row.kind {
	case rowProject:
		return row.project.Name
	case rowEntry, rowSession:
		return row.entry.Title
	}
	return ""
}

func (r *Rail) tooltip(w kit.WindowProjects, row sideRow, wide bool) string {
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
		if k := kit.BoundKey(r.h, cmd, args); k != "" {
			tip += "  " + k
		}
		if r.unavailable(row) {
			tip += " (not available yet)"
		}
		if row.entry.Scope == uireg.ScopeGlobalEntry && !wide {
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

func (r *Rail) activate(w kit.WindowProjects, row sideRow) {
	ctx := context.Background()
	if uireg.PlaceShown(r.h) {
		uireg.ClosePlaces(r.h)
		if row.kind != rowProject && active(r.h, row.entry, row.scope) {
			return
		}
	}
	var err error
	switch row.kind {
	case rowProject:
		if row.project.Name == w.Open {
			err = r.h.Run(ctx, "project.fold", nil)
			break
		}
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

// stateLine is a project's header line: branch and ahead/behind, its changes ("read-only" for a project the session
// only reads), then what plugins add (a PR, CI).
func (r *Rail) stateLine(w kit.WindowProjects, p core.Project) stateLine {
	th := r.h.CurrentTheme()
	if th == nil {
		return nil
	}
	faint := th.C(theme.FgFaint)
	var out stateLine
	st := kit.GitOf(r.h).Tree(p.Dir).State()
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
		if p.ReadOnly {
			out = append(out, statePart{text: "read-only", tone: faint})
		} else if n := kit.UncommittedCount(st); n > 0 {
			out = append(out, statePart{text: fmt.Sprintf("%d changed", n), tone: th.C(tSideChanged)})
		}
	}
	parts := uireg.ProjectStateParts(r.h, p.Name, p.Dir)
	for _, s := range parts {
		out = append(out, statePart{text: s.Text, tone: kit.TokenColour(th, s.Style, faint), dot: s.Dot})
	}
	if st != nil && !p.ReadOnly && kit.UncommittedCount(st) == 0 && len(parts) == 0 {
		out = append(out, statePart{text: "clean", tone: faint})
	}
	return out
}

// wideRow draws a row of the wide sidebar.
func (r *Rail) wideRow(c *ui.Ctx, w kit.WindowProjects, row sideRow) {
	th := c.Theme()
	switch row.kind {
	case rowHeading:
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(28), Pad: ui.Insets{L: 12, R: 6}, Gap: 4, Cross: ui.Center}, func(c *ui.Ctx) {
			label := "PROJECTS"
			if !w.Many() {
				label = strings.ToUpper(w.Open)
				if w.Session == nil || w.Session.InPlace() {
					label = kit.TildePath(w.Dir(""))
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
		widget.Rule(c, "rule")
		c.Box("pad", ui.Style{H: ui.Px(4)}, nil)
	case rowProject:
		r.header(c, w, row)
	case rowEntry, rowSession:
		r.wideEntry(c, row)
	case rowDivider:
		c.Box("div", ui.Style{W: ui.Flex(1), H: ui.Px(11), Pad: ui.Insets{L: 10, R: 10, T: 5, B: 5}}, func(c *ui.Ctx) {
			c.Box("line", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tSideDivider)}, nil)
		})
	case rowGap:
		c.Box("gap", ui.Style{H: ui.Flex(1)}, nil)
	}
}

// addButton is + in the sidebar's heading: add a project to the session.
func (r *Rail) addButton(c *ui.Ctx, w kit.WindowProjects) {
	if r.canAdd(w) && widget.IconButton(c, "add", "+", widget.IconButtonOpts{Label: "Add a project", Help: "shell/rail", Size: 20}) {
		if err := r.h.Run(context.Background(), "project.add", nil); err != nil {
			r.h.Log().Error("sidebar", "add", err)
		}
	}
}

// header is a project's two lines: twisty, colour, name and ⌃n; then its state.
func (r *Rail) header(c *ui.Ctx, w kit.WindowProjects, row sideRow) {
	th := c.Theme()
	open := row.project.Name == w.Open
	fill := render.Color{}
	if open {
		fill = th.C(tSideOpen)
	}
	c.Column("project", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 10, R: 8, T: 5, B: 5}, Gap: 2, Fill: fill}, func(c *ui.Ctx) {
		c.Row("l1", ui.Style{W: ui.Flex(1), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
			twisty, name := "▸", th.C(tSideFolded)
			if open {
				name = th.C(tSideName)
				if !w.Folded() {
					twisty = "▾"
				}
			}
			c.Box("tw", ui.Style{W: ui.Px(9)}, func(c *ui.Ctx) { c.Text("t", twisty, kit.Mono(11, typeset.Normal, th.C(tSideHead))) })
			c.Box("sq", ui.Style{W: ui.Px(8), H: ui.Px(8), Radius: 2, Fill: w.Colour(th, row.project.Name)}, nil)
			c.Text("n", row.project.Name, kit.Mono(11, typeset.SemiBold, name))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if row.index < 9 {
				c.Box("key", ui.Style{Pad: ui.PadXY(4, 1), Radius: 3, Stroke: th.C(tSideKeyEdge), Border: 1}, func(c *ui.Ctx) {
					c.Text("k", fmt.Sprintf("⌃%d", row.index+1), kit.Mono(9, typeset.Normal, th.C(tSideKey)))
				})
			}
		})
		line := r.stateLine(w, row.project)
		c.Row("l2", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 15}, Gap: 4, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
			for i, p := range line {
				if i > 0 && !p.dot && !strings.HasPrefix(p.text, "↑") && !strings.HasPrefix(p.text, "↓") {
					c.Text(fmt.Sprint("sep", i), "·", kit.Mono(10, typeset.Normal, th.C(tSideHead)))
				}
				if p.dot {
					c.Box(fmt.Sprint("dot", i), ui.Style{W: ui.Px(6), H: ui.Px(6), Radius: 3, Fill: p.tone}, nil)
				}
				c.Text(i, p.text, kit.Mono(10, typeset.Normal, p.tone))
			}
		})
	})
}

// entryNote is the right of an entry: its badge as a pill, else its note.
func (r *Rail) entryNote(c *ui.Ctx, row sideRow, size float32) {
	th := c.Theme()
	if n := badge(row.entry); n > 0 {
		widget.Badge(c, "badge", n, widget.BadgeOpts{Fill: badgeFill(th, row.entry), Ink: th.C(tRailBadgeF), Size: size})
		return
	}
	if note := noteOf(row); note != "" {
		c.Text("note", note, kit.Mono(10, typeset.Normal, th.C(tSideNote)))
	}
}

func (r *Rail) wideEntry(c *ui.Ctx, row sideRow) {
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
			entryIcon(c, row.entry, 13, icol)
			c.Text("t", row.entry.Title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: lcol})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			r.entryNote(c, row, 9)
		})
	})
}

// narrowRow draws a row of the icon strip.
func (r *Rail) narrowRow(c *ui.Ctx, w kit.WindowProjects, row sideRow) {
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
		col := w.Colour(th, row.project.Name)
		open := row.project.Name == w.Open
		cell("project", 28, func(c *ui.Ctx) {
			st := ui.Style{W: ui.Px(30), H: ui.Px(24), Radius: 6, Fill: th.C(tSideInitBg), Main: ui.Center, Cross: ui.Center}
			if open {
				st.Stroke, st.Border = col, 1
			}
			c.Box("ini", st, func(c *ui.Ctx) {
				c.Text("t", kit.ProjectInitials(row.project.Name), kit.Mono(10, typeset.Bold, col))
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
				entryIcon(c, row.entry, 16, col)
				if n := badge(row.entry); n > 0 {
					c.Box("badge", ui.Style{Float: &ui.Float{At: render.Point{X: 1}, Self: render.Point{X: 1}, Offset: render.Point{X: -2, Y: 3}}}, func(c *ui.Ctx) {
						widget.Badge(c, "pill", n, widget.BadgeOpts{Fill: badgeFill(th, row.entry), Ink: th.C(tRailBadgeF), Size: 8.5})
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

// entryIcon is an entry's icon: drawn by the entry, else its Icon.
func entryIcon(c *ui.Ctx, e uireg.RailEntry, size float32, tone render.Color) {
	if e.Draw != nil {
		e.Draw(c, "icon", size, tone)
		return
	}
	r, _ := ui.Service[registry.Registry](c)
	kit.Icon(c, r, "icon", e.Icon, size, tone)
}

// badgeFill is the colour of an entry's pill: its BadgeTone, else rail.badge.
func badgeFill(th *theme.Resolved, e uireg.RailEntry) render.Color {
	return kit.TokenColour(th, e.BadgeTone, th.C(tRailBadge))
}

func badge(e uireg.RailEntry) int {
	if e.Badge == nil {
		return 0
	}
	n := 0
	registry.Call(func() error { n = e.Badge(); return nil })
	return n
}
