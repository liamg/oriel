package shell_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui/layout"
)

// sidebarSize is a slice of the window as 42 draws each case.
var sidebarSize = render.Point{X: 760, Y: 420}

// fixtureRepo is a clone of a bare origin with files committed on main, then on branch (tracking origin/main) the
// commits ahead, each touching a file; remote false drops the origin.
func fixtureRepo(t *testing.T, root, name, branch string, files map[string]string, ahead int, remote bool) string {
	t.Helper()
	origin := filepath.Join(root, "origins", name+".git")
	dir := filepath.Join(root, name)
	rig.Git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	rig.Git(t, root, "clone", "-q", origin, dir)
	rig.Git(t, dir, "checkout", "-q", "-b", "main")
	rig.WriteFiles(t, dir, files)
	rig.Git(t, dir, "add", "-A")
	rig.Git(t, dir, "commit", "-q", "-m", "first")
	rig.Git(t, dir, "push", "-q", "origin", "main")
	if branch != "main" {
		rig.Git(t, dir, "checkout", "-q", "-b", branch, "--track", "origin/main")
	}
	for i := range ahead {
		rig.WriteFiles(t, dir, map[string]string{"NOTES": strings.Repeat("x\n", i+1)})
		rig.Git(t, dir, "add", "-A")
		rig.Git(t, dir, "commit", "-q", "-m", "more")
	}
	if !remote {
		rig.Git(t, dir, "remote", "remove", "origin")
	}
	return dir
}

// sidebarFixture is three projects: admin-ui two ahead on eng-2310 with four files changed, api on
// rate-limits, web on main, both clean.
func sidebarFixture(t *testing.T) []core.Project {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	root := t.TempDir()
	admin := fixtureRepo(t, root, "admin-ui", "eng-2310", map[string]string{
		"src/components/FilterBar.tsx": "export function FilterBar() {\n  return null\n}\n",
		"src/components/Table.tsx":     "export const Table = 1\n",
		"src/hooks/useFilters.ts":      "export function useFilters() {}\n",
		"src/api.ts":                   "export const api = 'needle'\n",
		"src/routes.ts":                "export const routes = []\n",
		"package.json":                 "{}\n",
		"vite.config.ts":               "export default {}\n",
	}, 2, true)
	rig.WriteFiles(t, admin, map[string]string{
		"src/components/FilterBar.tsx":  "export function FilterBar() {\n  return 'saved'\n}\n",
		"src/components/FilterChip.tsx": "export const FilterChip = 1\n",
		"src/api.ts":                    "export const api = 'needle, again'\n",
		"src/routes.ts":                 "export const routes = ['filters']\n",
	})
	api := fixtureRepo(t, root, "api", "rate-limits", map[string]string{
		"proto/usage.proto":  "package usage.v1;\n\nmessage Filter {\n  string field = 1;\n  string needle = 2;\n}\n",
		"limiter/limiter.go": "package limiter\n",
	}, 0, true)
	web := fixtureRepo(t, root, "web", "main", map[string]string{"index.html": "<html></html>\n"}, 0, true)
	return []core.Project{{Name: "admin-ui", Dir: admin, Branch: "eng-2310", Primary: true}, {Name: "api", Dir: api, Branch: "rate-limits"},
		{Name: "web", Dir: web, Branch: "main"}}
}

// sessionScene is the window on a session in front with projects, the first one's directory open, at size.
func sessionScene(t *testing.T, kind core.Kind, projects []core.Project, size render.Point) *apptest.Scene {
	t.Helper()
	s := apptest.New(t, apptest.Options{WorkDir: projects[0].Dir, Size: size})
	threads := []core.Thread{{ID: "t1", Status: core.ThreadOpen}, {ID: "t2", Status: core.ThreadOpen}, {ID: "t3", Status: core.ThreadOpen}}
	for _, ev := range []core.Event{
		core.SessionSynced{Session: core.SessionState{ID: "eng-2310", Name: "ENG-2310 admin filters", Kind: kind, Status: core.StatusWorking,
			Projects: projects, Threads: threads}},
		core.SessionFocused{ID: "eng-2310"},
	} {
		if err := s.Store().Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.Registry().Contribute(uireg.ProjectState{Name: "test.pr", State: func(p, dir string) []uireg.StatePart {
		switch p {
		case "admin-ui":
			return []uireg.StatePart{{Text: "no PR"}}
		case "api":
			return []uireg.StatePart{{Text: "#790"}, {Text: "CI failing", Style: "error", Dot: true}}
		}
		return nil
	}})
	s.SettleGit()
	return s
}

func openProject(s *apptest.Scene) string {
	se, _ := s.Store().State().Session("eng-2310")
	return se.Snapshot().OpenProject()
}

func visibleRefs(s *apptest.Scene) []layout.ViewRef { return s.Host().Layout().Visible() }

// The sidebar's layouts: three projects wide with admin-ui's Explorer and a file from api
// beside one of admin-ui's; narrow on api's Search; one project; a directory opened directly.
func TestSidebarSnapshots(t *testing.T) {
	t.Run("three-projects", func(t *testing.T) {
		ps := sidebarFixture(t)
		s := sessionScene(t, core.KindWork, ps, sidebarSize)
		s.Run("project.panel", registry.Args{"name": "explorer"})
		s.Run("editor.open", registry.Args{"path": filepath.Join(ps[0].Dir, "src/components/FilterBar.tsx")})
		s.Run("editor.open", registry.Args{"path": filepath.Join(ps[1].Dir, "proto/usage.proto")})
		s.SettleGit()
		s.WaitHighlights()
		s.Quiet() // the gutter's signs and the cursor line's blame, read off the loop, are in
		s.Golden("sidebar-three")
		text := s.Text()
		for _, want := range []string{"PROJECTS", "admin-ui", "eng-2310", "↑2", "4 changed", "no PR", "api", "rate-limits", "#790",
			"CI failing", "web", "clean", "Explorer", "Search", "Git", "Pull request", "Tests", "Requests", "3", "Ship", "Settings",
			"admin-ui\nEXPLORER", "⌃1", "⌃2", "⌃3"} {
			if !strings.Contains("\n"+text+"\n", "\n"+want+"\n") && !strings.Contains(text, want) {
				t.Errorf("no %q in the sidebar:\n%s", want, text)
			}
		}
	})
	t.Run("narrow", func(t *testing.T) {
		ps := sidebarFixture(t)
		s := sessionScene(t, core.KindWork, ps, sidebarSize)
		s.Run("sidebar.form", registry.Args{"form": "narrow"})
		s.Run("project.open", registry.Args{"project": "api"})
		s.Run("project.panel", registry.Args{"name": "search"})
		s.Golden("sidebar-narrow")
		text := s.Text()
		for _, want := range []string{"AD", "AP", "WE", "»", "api\nSEARCH"} {
			if !strings.Contains(text, want) {
				t.Errorf("no %q in the narrow sidebar:\n%s", want, text)
			}
		}
	})
	t.Run("one-project", func(t *testing.T) {
		ps := sidebarFixture(t)
		s := sessionScene(t, core.KindWork, ps[:1], sidebarSize)
		s.Run("sidebar.form", registry.Args{"form": "wide"})
		s.Run("project.panel", registry.Args{"name": "explorer"})
		s.SettleGit()
		s.Golden("sidebar-one")
		text := s.Text()
		if !strings.Contains(text, "ADMIN-UI") || strings.Contains(text, "⌃1") {
			t.Errorf("one project: a labelled list, no header:\n%s", text)
		}
		if !strings.Contains(text, "Pull request") || !strings.Contains(text, "Ship") {
			t.Errorf("one project with a remote: Pull request and Ship are there:\n%s", text)
		}
	})
	t.Run("directory", func(t *testing.T) {
		t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
		s := apptest.New(t, apptest.Options{Size: sidebarSize, Config: func(cfg *apptest.Config) {
			src := filepath.Join(os.Getenv("HOME"), "src")
			os.MkdirAll(src, 0o755)
			cfg.WorkDir = fixtureRepo(t, src, "blog", "main", map[string]string{"posts/2026-09-vim.md": "# vim\n", "config.toml": "\n"}, 0, false)
		}})
		s.Run("sidebar.form", registry.Args{"form": "wide"})
		s.Run("project.panel", registry.Args{"name": "explorer"})
		s.SettleGit()
		s.Golden("sidebar-directory")
		text := s.Text()
		if !strings.Contains(text, "Explorer") || strings.Contains(text, "Pull request") || strings.Contains(text, "Ship") {
			t.Errorf("a directory with no remote has no Pull request or Ship:\n%s", text)
		}
	})
}

// The pages every session shares sit at the sidebar's foot in both forms, apart from the session's own entries.
func TestSidebarGlobalEntriesAtFoot(t *testing.T) {
	size := render.Point{X: 760, Y: 900}
	for _, form := range []string{"wide", "narrow"} {
		t.Run(form, func(t *testing.T) {
			s := sessionScene(t, core.KindWork, sidebarFixture(t)[:1], size)
			s.Run("sidebar.form", registry.Args{"form": form})
			s.Frame()
			ship, settings := s.Node("Ship").Bounds, s.Node("Settings").Bounds
			if foot := settings.Max.Y; foot < size.Y-60 {
				t.Errorf("Settings ends at %v in a %v-high window, not at the foot", foot, size.Y)
			}
			if gap := s.Node("Repos").Bounds.Min.Y - ship.Max.Y; gap < 200 {
				t.Errorf("the global entries start %v below Ship; want them apart", gap)
			}
		})
	}
}

// A header's click or ⌃n opens its project and folds the other; switching back reopens the panel it had, and
// j/k/⏎ in the sidebar do the same.
func TestSidebarSwitchesProjects(t *testing.T) {
	ps := sidebarFixture(t)
	s := sessionScene(t, core.KindWork, ps, sidebarSize)
	s.Click(apptest.Centre(s.Node("Explorer").Bounds), 0)
	if !slices.Contains(visibleRefs(s), layout.ViewRef{ID: kit.ExplorerView, Title: "Explorer", Project: "admin-ui"}) {
		t.Fatalf("admin-ui's Explorer isn't showing: %v", visibleRefs(s))
	}
	s.Click(apptest.Centre(s.Node("api").Bounds), 0)
	if got := openProject(s); got != "api" {
		t.Fatalf("open project %q after clicking api", got)
	}
	if n := strings.Count(s.Text(), "\nExplorer\n"); n != 1 {
		t.Errorf("admin-ui didn't fold: %d Explorer entries", n)
	}
	for _, v := range visibleRefs(s) {
		if v.Project == "admin-ui" {
			t.Errorf("admin-ui's %s is still in the dock", v.ID)
		}
	}
	s.Run("project.panel", registry.Args{"name": "search"})
	s.Press("<C-1>")
	if got := openProject(s); got != "admin-ui" {
		t.Fatalf("⌃1 opened %q", got)
	}
	if !strings.Contains(s.Text(), "admin-ui\nEXPLORER") {
		t.Errorf("admin-ui's Explorer didn't come back under its header:\n%s", s.Text())
	}
	s.Press("<C-2>")
	if !strings.Contains(s.Text(), "api\nSEARCH") {
		t.Errorf("api's Search didn't come back under its header:\n%s", s.Text())
	}
	// From the keyboard: the focused sidebar's j/k move over headers and entries, ⏎ opens.
	s.Click(apptest.Centre(s.Node("admin-ui").Bounds), 0)
	for range 7 {
		s.UI().Key(render.KeyEvent{Key: "j"})
		s.Frame()
	}
	s.UI().Key(render.KeyEvent{Key: "CR"})
	s.Frame()
	if got := openProject(s); got != "api" {
		t.Errorf("j ×7 ⏎ from admin-ui, over its six entries to api, opened %q", got)
	}
}

// The sidebar is the dock's chooser: a project's panel, Requests and Plan docs each open in the dock by itself, under
// a header, never as one tab among others, and the rail lights the one showing.
func TestSidebarPanelsReplaceEachOther(t *testing.T) {
	ps := sidebarFixture(t)
	s := sessionScene(t, core.KindWork, ps, sidebarSize)
	planning(t, s)
	s.Click(apptest.Centre(s.Node("Explorer").Bounds), 0)
	s.Click(apptest.Centre(s.Node("Search").Bounds), 0)
	if got := s.Host().Layout().Docks[layout.Left].Leaf.Tabs; len(got) != 1 || got[0].ID != kit.SearchView {
		t.Fatalf("left dock after Explorer then Search: %v, want Search alone", got)
	}
	if strings.Contains(s.Text(), "admin-ui · Search") || !strings.Contains(s.Text(), "admin-ui\nSEARCH") {
		t.Errorf("the panel isn't under a header:\n%s", s.Text())
	}
	for _, p := range []struct{ entry, view, scope string }{
		{"explorer", kit.ExplorerView, "admin-ui"}, {"requests", kit.RequestsView, ""}, {"plan-docs", kit.PlanDocsView, ""}, {"search", kit.SearchView, "admin-ui"},
	} {
		e := railEntry(t, s, p.entry)
		s.Click(apptest.Centre(s.Node(e.Title).Bounds), 0)
		s.Frame()
		if got := s.Host().Layout().Docks[layout.Left].Leaf.Tabs; len(got) != 1 || got[0].ID != p.view {
			t.Errorf("left dock after %s: %v, want %s alone", e.Title, got, p.view)
		}
		if !shell.RailEntryActive(s.Host(), e, p.scope) {
			t.Errorf("%s is showing, and its rail entry isn't lit", e.Title)
		}
		if k := kit.KeyLabel("<D-R>"); p.entry == "requests" && !strings.Contains(s.Text(), "⌕ search requests…\n"+k) {
			t.Errorf("Requests doesn't show its key %s:\n%s", k, s.Text())
		}
	}
}

// planning makes the scene's session one that's planning.
func planning(t *testing.T, s *apptest.Scene) {
	t.Helper()
	se, _ := s.Store().State().Session("eng-2310")
	st := se.Snapshot()
	st.StartWork = true
	if err := s.Store().Apply(core.SessionSynced{Session: st}); err != nil {
		t.Fatal(err)
	}
	s.Frame()
}

func railEntry(t *testing.T, s *apptest.Scene, name string) uireg.RailEntry {
	t.Helper()
	for _, e := range uireg.RailEntries(s.Registry()) {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no rail entry %q", name)
	return uireg.RailEntry{}
}

// Plan docs is on the rail only while the session in front is planning, and its entry shows and hides the panel.
func TestPlanDocsOnTheRail(t *testing.T) {
	ps := sidebarFixture(t)
	s := sessionScene(t, core.KindWork, ps, sidebarSize)
	if strings.Contains(s.Text(), "Plan docs") {
		t.Fatalf("Plan docs is on the rail of a session that isn't planning:\n%s", s.Text())
	}
	planning(t, s)
	if !strings.Contains(s.Text(), "Plan docs") {
		t.Fatalf("no Plan docs on a planning session's rail:\n%s", s.Text())
	}
	shown := func() bool { return kit.Showing(s.Host(), kit.PlanDocsView, "") }
	if shown() {
		s.Click(apptest.Centre(s.Node("Plan docs").Bounds), 0)
		s.Frame()
	}
	s.Click(apptest.Centre(s.Node("Plan docs").Bounds), 0)
	s.Frame()
	if !shown() {
		t.Fatal("clicking Plan docs didn't show the panel")
	}
	s.Click(apptest.Centre(s.Node("Plan docs").Bounds), 0)
	s.Frame()
	if shown() {
		t.Error("clicking Plan docs again didn't hide the panel")
	}
}

// ⌘⇧F opens the open project's Search, which searches that project; All projects adds the others', by project.
func TestSidebarSearchIsPerProject(t *testing.T) {
	ps := sidebarFixture(t)
	s := sessionScene(t, core.KindWork, ps, apptest.MockupSize)
	s.Press("<D-F>")
	if !slices.ContainsFunc(visibleRefs(s), func(v layout.ViewRef) bool { return v.ID == kit.SearchView && v.Project == "admin-ui" }) {
		t.Fatalf("⌘⇧F didn't open admin-ui's Search: %v", visibleRefs(s))
	}
	s.TypeInto("Find", "needle")
	text := s.WaitSearch("api.ts")
	if strings.Contains(text, "usage.proto") {
		t.Errorf("admin-ui's search found api's file:\n%s", text)
	}
	s.Click(apptest.Centre(s.Node("All projects").Bounds), 0)
	text = s.WaitSearch("usage.proto")
	_, panel, _ := strings.Cut(text, "admin-ui\nSEARCH")
	i, j := strings.Index(panel, "\nadmin-ui\n"), strings.Index(panel, "\napi\n")
	if i < 0 || j < i || !strings.Contains(panel[i:j], "src/api.ts") || !strings.Contains(panel[j:], "proto/usage.proto") {
		t.Errorf("all projects' matches aren't grouped by project:\n%s", text)
	}
}

// A plugin's project entry from Lua is listed under every project and opens its view for that project; a
// session entry goes below the divider; both go when the config drops them.
func TestSidebarPluginEntries(t *testing.T) {
	ps := sidebarFixture(t)
	s := sessionScene(t, core.KindWork, ps, sidebarSize)
	s.WriteConfig(map[string]string{"init.lua": `
oriel.rail.add{ name = "tickets", scope = "project", icon = "◆", title = "Tickets", command = "view.toggle",
  args = { id = "tickets", place = "dock", edge = "left", title = "Tickets" }, order = 70 }
oriel.rail.add{ name = "inbox", scope = "session", icon = "✉", title = "Inbox", command = "Settings", order = 45 }
oriel.view.override("tickets", function(ctx) return oriel.ui.text("tickets of " .. ctx.project) end)
`})
	s.ReloadLua()
	s.Frame()
	s.Frame()
	if !strings.Contains(s.Text(), "Tickets") {
		t.Fatalf("no Tickets under admin-ui:\n%s", s.Text())
	}
	text := s.Text()
	if r, i := strings.Index(text, "\nRequests\n"), strings.Index(text, "\nInbox\n"); i < r || r < 0 {
		t.Errorf("Inbox isn't with the session's entries:\n%s", text)
	}
	s.Run("project.open", registry.Args{"project": "api"})
	s.Frame()
	s.Click(apptest.Centre(s.Node("Tickets").Bounds), 0)
	if !slices.ContainsFunc(visibleRefs(s), func(v layout.ViewRef) bool { return v.ID == "tickets" && v.Project == "api" }) {
		t.Fatalf("Tickets didn't open for api: %v", visibleRefs(s))
	}
	if !strings.Contains(s.Text(), "tickets of api") {
		t.Errorf("the view doesn't know its project:\n%s", s.Text())
	}
	s.WriteConfig(map[string]string{"init.lua": ""})
	s.ReloadLua()
	s.Frame()
	names := []string{}
	for _, e := range uireg.RailEntries(s.Registry()) {
		names = append(names, e.Name)
	}
	if slices.Contains(names, "tickets") || slices.Contains(names, "inbox") {
		t.Errorf("the entries outlived the config: %v", names)
	}
}

// A file from another project opens in the one tab strip, led by its project's colour, its tooltip and
// breadcrumb naming the project; the sidebar stays where it was.
func TestSidebarTabsAcrossProjects(t *testing.T) {
	ps := sidebarFixture(t)
	s := sessionScene(t, core.KindWork, ps, apptest.MockupSize)
	s.Run("editor.open", registry.Args{"path": filepath.Join(ps[0].Dir, "src/components/FilterBar.tsx")})
	s.Run("editor.open", registry.Args{"path": filepath.Join(ps[1].Dir, "proto/usage.proto")})
	s.Frame()
	if got := openProject(s); got != "admin-ui" {
		t.Errorf("opening api's file moved the sidebar to %q", got)
	}
	var tabs []layout.ViewRef
	for _, v := range s.Host().Layout().Shown() {
		tabs = append(tabs, v)
	}
	leaf := s.Host().Layout().Find(tabs[0]).Leaf
	if len(leaf.Tabs) != 2 {
		t.Fatalf("one tab strip: %v", leaf.Tabs)
	}
	violet := kit.TokenColour(s.UI().Theme(), "violet", render.Color{})
	if c, ok := shell.PaneLead(s.Headless, leaf.Tabs[1]); !ok || c != violet {
		t.Errorf("usage.proto's tab leads with %v %v, want api's violet", c, ok)
	}
	if tip := shell.PaneTip(s.Headless, leaf.Tabs[1]); !strings.Contains(tip, "api") || !strings.Contains(tip, "proto/usage.proto") {
		t.Errorf("tooltip %q", tip)
	}
	if !strings.Contains(s.Text(), "api\n›\nproto\n›\nusage.proto") {
		t.Errorf("the breadcrumb doesn't start with the project:\n%s", s.Text())
	}
}

// Clicking the open entry hides the dock; the form is the window's and comes back after a restart; a
// directory without a remote has no Pull request; an entry is dimmed until its view is registered (Debug), and Pull
// request and Ship are lit.
func TestSidebarFormDockAndAvailability(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	state := t.TempDir()
	dir := fixtureRepo(t, t.TempDir(), "blog", "main", map[string]string{"a.md": "a\n"}, 0, false)
	r := apptest.NewWindow(t, apptest.WindowOpts{State: state, WorkDir: dir})
	run := func(id string, args registry.Args) {
		t.Helper()
		if err := r.Do(func() error { return r.Host.Run(context.Background(), id, args) }); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	run("sidebar.form", registry.Args{"form": "wide"})
	r.WaitText("Explorer")
	r.ClickText("Explorer")
	r.WaitText("ALL")
	r.ClickText("Explorer")
	r.WaitFrame("the dock hidden", func(s string) bool { return !strings.Contains(s, "ALL") })
	if strings.Contains(r.WaitText("Explorer"), "Pull request") {
		t.Error("a directory without a remote lists Pull request")
	}
	run("sidebar.form", registry.Args{"form": "narrow"})
	r.WaitText("»")
	r.Stop()
	r2 := apptest.NewWindow(t, apptest.WindowOpts{State: state, WorkDir: dir})
	r2.WaitText("»")
	r2.Stop()

	ps := sidebarFixture(t)
	s := sessionScene(t, core.KindWork, ps, sidebarSize)
	if s.Node("Pull request").Disabled || s.Node("Ship").Disabled {
		t.Error("Pull request and Ship are dimmed, though their views are registered")
	}
	if s.Node("Debug").Disabled {
		t.Error("Debug is dimmed, though its view is registered")
	}
}

// The user adds a project to the session: + in the sidebar's header picks a repo or a typed path, and the
// project is added and opened.
func TestSidebarAddsAProject(t *testing.T) {
	lw := rig.NewHost(t)
	billing := filepath.Join(t.TempDir(), "billing")
	rig.Git(t, filepath.Dir(billing), "init", "-q", "-b", "main", billing)
	rig.Git(t, billing, "commit", "-q", "--allow-empty", "-m", "first")
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	rig.WaitState(t, r.Store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	err := r.Do(func() error {
		return r.Host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "rounding", "repos": []string{lw.Repo}})
	})
	if err != nil {
		t.Fatal(err)
	}
	rig.WaitState(t, r.Store, "the session in front", func(st core.State) bool { return st.Focus == "rounding" })
	r.WaitFrame("the window on the session", func(s string) bool {
		return strings.Contains(s, "+") && !strings.Contains(s, "Open Directory") && !strings.Contains(s, "Opening rounding")
	})
	r.ClickAccess("Add a project")
	r.WaitText("ADD A PROJECT")
	r.TypeText(billing)
	r.WaitAccess("the row adding billing", func(w *apptest.Window) bool { return w.Access("Add "+billing) != nil }) // its text may be cut
	r.TypeKeys("<CR>")
	st := rig.WaitState(t, r.Store, "billing in the session", func(st core.State) bool {
		se, ok := st.Session("rounding")
		return ok && len(se.Projects()) == 2
	})
	se, _ := st.Session("rounding")
	if names := []string{se.Projects()[0].Name, se.Projects()[1].Name}; names[1] != "billing" {
		t.Fatalf("projects %v", names)
	}
	rig.WaitState(t, r.Store, "billing open", func(st core.State) bool {
		se, _ := st.Session("rounding")
		return se.Snapshot().OpenProject() == "billing"
	})
	r.WaitFrame("both projects' headers", func(s string) bool { return apptest.HasAll(s, "pricing", "billing", "⌃2") })
}

// A plan's agent reads the user's checkouts, protected from edits: their uncommitted work isn't the session's, so neither Git's count nor the header counts it.
func TestSidebarReadOnlyProjectCountsNoChanges(t *testing.T) {
	ps := sidebarFixture(t)
	ps[0].ReadOnly = true
	s := sessionScene(t, core.KindPlan, ps, sidebarSize)
	planning(t, s)
	if n := railEntry(t, s, "git").Note("admin-ui"); n != "" {
		t.Errorf("Git's count on a read-only project: %q", n)
	}
	if got := s.Text(); strings.Contains(got, "4 changed") || !strings.Contains(got, "read-only") {
		t.Errorf("the read-only project's header:\n%s", got)
	}
	if n := railEntry(t, s, "git").Note("api"); n != "" {
		t.Errorf("Git's count on a clean project: %q", n)
	}
}

// A click on the open project's header folds its panels away and another brings them back; another project's opens
// it, unfolded. A header dragged onto another moves the project to its place, and ⌃n follows the sidebar's order.
func TestSidebarProjectsFoldAndMove(t *testing.T) {
	ps := sidebarFixture(t)
	s := sessionScene(t, core.KindWork, ps, sidebarSize)
	panels := func() int { return strings.Count(s.Text(), "\nExplorer\n") }
	header := func(name string) {
		s.Click(apptest.Centre(s.Node(name).Bounds), 0)
		s.Frame()
	}
	if n := panels(); n != 1 {
		t.Fatalf("admin-ui's panels to start: %d Explorer entries", n)
	}
	header("admin-ui")
	if n := panels(); n != 0 || openProject(s) != "admin-ui" {
		t.Fatalf("a click on the open project: %d Explorer entries, %q open", n, openProject(s))
	}
	header("admin-ui")
	if n := panels(); n != 1 {
		t.Fatalf("a second click didn't unfold it: %d Explorer entries", n)
	}
	header("admin-ui")
	header("api")
	if n := panels(); n != 1 || openProject(s) != "api" {
		t.Fatalf("api opened from admin-ui folded: %d Explorer entries, %q open", n, openProject(s))
	}

	s.Drag(apptest.Centre(s.Node("web").Bounds), apptest.Centre(s.Node("admin-ui").Bounds))
	se, _ := s.Store().State().Session("eng-2310")
	var names []string
	for _, p := range se.Snapshot().ListedProjects() {
		names = append(names, p.Name)
	}
	if !slices.Equal(names, []string{"web", "admin-ui", "api"}) {
		t.Fatalf("the projects after web was dragged onto admin-ui: %v", names)
	}
	if openProject(s) != "api" {
		t.Errorf("the drag opened %q", openProject(s))
	}
	if i, j := strings.Index(s.Text(), "\nweb\n"), strings.Index(s.Text(), "\nadmin-ui\n"); i < 0 || j < 0 || i > j {
		t.Errorf("the sidebar doesn't list web first:\n%s", s.Text())
	}
	s.Press("<C-1>")
	if got := openProject(s); got != "web" {
		t.Errorf("⌃1 opened %q, want web, first now", got)
	}
}
