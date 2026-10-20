package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui/layout"
)

// sidebarSize is a slice of the window as 42 draws each case.
var sidebarSize = render.Point{X: 760, Y: 420}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// fixtureRepo is a clone of a bare origin with files committed on main, then on branch (tracking origin/main) the
// commits ahead, each touching a file; remote false drops the origin.
func fixtureRepo(t *testing.T, root, name, branch string, files map[string]string, ahead int, remote bool) string {
	t.Helper()
	origin := filepath.Join(root, "origins", name+".git")
	dir := filepath.Join(root, name)
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	git(t, root, "clone", "-q", origin, dir)
	git(t, dir, "checkout", "-q", "-b", "main")
	writeFiles(t, dir, files)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "first")
	git(t, dir, "push", "-q", "origin", "main")
	if branch != "main" {
		git(t, dir, "checkout", "-q", "-b", branch, "--track", "origin/main")
	}
	for i := range ahead {
		writeFiles(t, dir, map[string]string{"NOTES": strings.Repeat("x\n", i+1)})
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-q", "-m", "more")
	}
	if !remote {
		git(t, dir, "remote", "remove", "origin")
	}
	return dir
}

// sidebarFixture is 42's three projects: admin-ui two ahead on eng-2310 with four files changed, api on
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
	writeFiles(t, admin, map[string]string{
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
func sessionScene(t *testing.T, kind core.Kind, projects []core.Project, size render.Point) *errScene {
	t.Helper()
	s := newScene(t, "", projects[0].Dir, size)
	threads := []core.Thread{{ID: "t1", Status: core.ThreadOpen}, {ID: "t2", Status: core.ThreadOpen}, {ID: "t3", Status: core.ThreadOpen}}
	for _, ev := range []core.Event{
		core.SessionSynced{Session: core.SessionState{ID: "eng-2310", Name: "ENG-2310 admin filters", Kind: kind, Status: core.StatusWorking,
			Projects: projects, Threads: threads}},
		core.SessionFocused{ID: "eng-2310"},
	} {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.reg.Contribute(registry.ProjectState{Name: "test.pr", State: func(p, dir string) []registry.StatePart {
		switch p {
		case "admin-ui":
			return []registry.StatePart{{Text: "no PR"}}
		case "api":
			return []registry.StatePart{{Text: "#790"}, {Text: "CI failing", Style: "error", Dot: true}}
		}
		return nil
	}})
	s.settleGit(t)
	return s
}

// settleGit frames until every project's git status has been read.
func (s *errScene) settleGit(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.frame()
		s.sh.git.wait()
		if s.sh.git.settled() {
			s.frame()
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the projects' git status never came")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (s *errScene) openProject() string {
	se, _ := s.store.State().Session("eng-2310")
	return se.Snapshot().OpenProject()
}

func (s *errScene) visibleRefs() []layout.ViewRef { return s.sh.layout.root.Visible() }

// SH-17: the sidebar against 42's panels 1 to 5: three projects wide with admin-ui's Explorer and a file from api
// beside one of admin-ui's; narrow on api's Search; one project; a directory opened directly.
func TestSidebarSnapshots(t *testing.T) {
	t.Run("three-projects", func(t *testing.T) {
		ps := sidebarFixture(t)
		s := sessionScene(t, core.KindWork, ps, sidebarSize)
		s.run("project.panel", registry.Args{"name": "explorer"})
		s.run("editor.open", registry.Args{"path": filepath.Join(ps[0].Dir, "src/components/FilterBar.tsx")})
		s.run("editor.open", registry.Args{"path": filepath.Join(ps[1].Dir, "proto/usage.proto")})
		s.settleGit(t)
		s.golden("sidebar-three")
		text := s.frameText()
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
		s.run("sidebar.form", registry.Args{"form": "narrow"})
		s.run("project.open", registry.Args{"project": "api"})
		s.run("project.panel", registry.Args{"name": "search"})
		s.golden("sidebar-narrow")
		text := s.frameText()
		for _, want := range []string{"AD", "AP", "WE", "»", "api\nSEARCH"} {
			if !strings.Contains(text, want) {
				t.Errorf("no %q in the narrow sidebar:\n%s", want, text)
			}
		}
	})
	t.Run("one-project", func(t *testing.T) {
		ps := sidebarFixture(t)
		s := sessionScene(t, core.KindWork, ps[:1], sidebarSize)
		s.run("sidebar.form", registry.Args{"form": "wide"})
		s.run("project.panel", registry.Args{"name": "explorer"})
		s.golden("sidebar-one")
		text := s.frameText()
		if !strings.Contains(text, "ADMIN-UI") || strings.Contains(text, "⌃1") {
			t.Errorf("one project: a labelled list, no header:\n%s", text)
		}
		if !strings.Contains(text, "Pull request") || !strings.Contains(text, "Ship") {
			t.Errorf("one project with a remote: Pull request and Ship are there:\n%s", text)
		}
	})
	t.Run("directory", func(t *testing.T) {
		t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
		s := newSceneWith(t, "", "", sidebarSize, func(cfg *Config) {
			src := filepath.Join(os.Getenv("HOME"), "src")
			os.MkdirAll(src, 0o755)
			cfg.WorkDir = fixtureRepo(t, src, "blog", "main", map[string]string{"posts/2026-09-vim.md": "# vim\n", "config.toml": "\n"}, 0, false)
		})
		s.run("sidebar.form", registry.Args{"form": "wide"})
		s.run("project.panel", registry.Args{"name": "explorer"})
		s.settleGit(t)
		s.golden("sidebar-directory")
		text := s.frameText()
		if !strings.Contains(text, "Explorer") || strings.Contains(text, "Pull request") || strings.Contains(text, "Ship") {
			t.Errorf("a directory with no remote has no Pull request or Ship:\n%s", text)
		}
	})
}

// SH-17: a header's click or ⌃n opens its project and folds the other; switching back reopens the panel it had, and
// j/k/⏎ in the sidebar do the same.
func TestSidebarSwitchesProjects(t *testing.T) {
	ps := sidebarFixture(t)
	s := sessionScene(t, core.KindWork, ps, sidebarSize)
	s.click(centre(s.access("Explorer").Bounds))
	if !slices.Contains(s.visibleRefs(), layout.ViewRef{ID: ExplorerView, Title: "Explorer", Project: "admin-ui"}) {
		t.Fatalf("admin-ui's Explorer isn't showing: %v", s.visibleRefs())
	}
	s.click(centre(s.access("api").Bounds))
	if got := s.openProject(); got != "api" {
		t.Fatalf("open project %q after clicking api", got)
	}
	if n := strings.Count(s.frameText(), "\nExplorer\n"); n != 1 {
		t.Errorf("admin-ui didn't fold: %d Explorer entries", n)
	}
	for _, v := range s.visibleRefs() {
		if v.Project == "admin-ui" {
			t.Errorf("admin-ui's %s is still in the dock", v.ID)
		}
	}
	s.run("project.panel", registry.Args{"name": "search"})
	s.keyCommand(t, "<C-1>")
	if got := s.openProject(); got != "admin-ui" {
		t.Fatalf("⌃1 opened %q", got)
	}
	if !strings.Contains(s.frameText(), "admin-ui\nEXPLORER") {
		t.Errorf("admin-ui's Explorer didn't come back under its header:\n%s", s.frameText())
	}
	s.keyCommand(t, "<C-2>")
	if !strings.Contains(s.frameText(), "api\nSEARCH") {
		t.Errorf("api's Search didn't come back under its header:\n%s", s.frameText())
	}
	// From the keyboard: the focused sidebar's j/k move over headers and entries, ⏎ opens.
	s.click(centre(s.access("admin-ui").Bounds))
	for range 6 {
		s.ui.Key(render.KeyEvent{Key: "j"})
		s.frame()
	}
	s.ui.Key(render.KeyEvent{Key: "CR"})
	s.frame()
	if got := s.openProject(); got != "api" {
		t.Errorf("j ×6 ⏎ from admin-ui, over its five entries to api, opened %q", got)
	}
}

// The sidebar is the dock's chooser: a project's panel, Requests and Plan docs each open in the dock by itself, under
// a header, never as one tab among others, and the rail lights the one showing.
func TestSidebarPanelsReplaceEachOther(t *testing.T) {
	ps := sidebarFixture(t)
	s := sessionScene(t, core.KindWork, ps, sidebarSize)
	planning(t, s)
	s.click(centre(s.access("Explorer").Bounds))
	s.click(centre(s.access("Search").Bounds))
	if got := s.sh.layout.root.Docks[layout.Left].Leaf.Tabs; len(got) != 1 || got[0].ID != SearchView {
		t.Fatalf("left dock after Explorer then Search: %v, want Search alone", got)
	}
	if strings.Contains(s.frameText(), "admin-ui · Search") || !strings.Contains(s.frameText(), "admin-ui\nSEARCH") {
		t.Errorf("the panel isn't under a header:\n%s", s.frameText())
	}
	for _, p := range []struct{ entry, view, scope string }{
		{"explorer", ExplorerView, "admin-ui"}, {"requests", RequestsView, ""}, {"plan-docs", PlanDocsView, ""}, {"search", SearchView, "admin-ui"},
	} {
		e := railEntry(t, s, p.entry)
		s.click(centre(s.access(e.Title).Bounds))
		s.frame()
		if got := s.sh.layout.root.Docks[layout.Left].Leaf.Tabs; len(got) != 1 || got[0].ID != p.view {
			t.Errorf("left dock after %s: %v, want %s alone", e.Title, got, p.view)
		}
		if !active(s.host, e, p.scope) {
			t.Errorf("%s is showing, and its rail entry isn't lit", e.Title)
		}
		if k := keyLabel("<D-R>"); p.entry == "requests" && !strings.Contains(s.frameText(), "REQUESTS\n"+k) {
			t.Errorf("Requests doesn't show its key %s:\n%s", k, s.frameText())
		}
	}
}

// planning makes the scene's session one that's planning.
func planning(t *testing.T, s *errScene) {
	t.Helper()
	se, _ := s.store.State().Session("eng-2310")
	st := se.Snapshot()
	st.StartWork = true
	if err := s.store.Apply(core.SessionSynced{Session: st}); err != nil {
		t.Fatal(err)
	}
	s.frame()
}

func railEntry(t *testing.T, s *errScene, name string) registry.RailEntry {
	t.Helper()
	for _, e := range registry.RailEntries(s.reg) {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no rail entry %q", name)
	return registry.RailEntry{}
}

// Plan docs is on the rail only while the session in front is planning, and its entry shows and hides the panel.
func TestPlanDocsOnTheRail(t *testing.T) {
	ps := sidebarFixture(t)
	s := sessionScene(t, core.KindWork, ps, sidebarSize)
	if strings.Contains(s.frameText(), "Plan docs") {
		t.Fatalf("Plan docs is on the rail of a session that isn't planning:\n%s", s.frameText())
	}
	planning(t, s)
	if !strings.Contains(s.frameText(), "Plan docs") {
		t.Fatalf("no Plan docs on a planning session's rail:\n%s", s.frameText())
	}
	shown := func() bool { return showing(s.host, PlanDocsView, "") }
	if shown() {
		s.click(centre(s.access("Plan docs").Bounds))
		s.frame()
	}
	s.click(centre(s.access("Plan docs").Bounds))
	s.frame()
	if !shown() {
		t.Fatal("clicking Plan docs didn't show the panel")
	}
	s.click(centre(s.access("Plan docs").Bounds))
	s.frame()
	if shown() {
		t.Error("clicking Plan docs again didn't hide the panel")
	}
}

// SH-17: ⌘⇧F opens the open project's Search, which searches that project; All projects adds the others', by project.
func TestSidebarSearchIsPerProject(t *testing.T) {
	ps := sidebarFixture(t)
	s := sessionScene(t, core.KindWork, ps, mockupSize)
	s.keyCommand(t, "<D-F>")
	if !slices.ContainsFunc(s.visibleRefs(), func(v layout.ViewRef) bool { return v.ID == SearchView && v.Project == "admin-ui" }) {
		t.Fatalf("⌘⇧F didn't open admin-ui's Search: %v", s.visibleRefs())
	}
	s.typeInto(t, "Find", "needle")
	text := s.waitSearch(t, "api.ts")
	if strings.Contains(text, "usage.proto") {
		t.Errorf("admin-ui's search found api's file:\n%s", text)
	}
	s.click(centre(s.access("All projects").Bounds))
	text = s.waitSearch(t, "usage.proto")
	_, panel, _ := strings.Cut(text, "admin-ui\nSEARCH")
	i, j := strings.Index(panel, "\nadmin-ui\n"), strings.Index(panel, "\napi\n")
	if i < 0 || j < i || !strings.Contains(panel[i:j], "src/api.ts") || !strings.Contains(panel[j:], "proto/usage.proto") {
		t.Errorf("all projects' matches aren't grouped by project:\n%s", text)
	}
}

// SH-17: a plugin's project entry from Lua is listed under every project and opens its view for that project; a
// session entry goes below the divider; both go when the config drops them.
func TestSidebarPluginEntries(t *testing.T) {
	ps := sidebarFixture(t)
	s := sessionScene(t, core.KindWork, ps, sidebarSize)
	s.writeConfig(t, map[string]string{"init.lua": `
oriel.rail.add{ name = "tickets", scope = "project", icon = "◆", title = "Tickets", command = "view.toggle",
  args = { id = "tickets", place = "dock", edge = "left", title = "Tickets" }, order = 70 }
oriel.rail.add{ name = "inbox", scope = "session", icon = "✉", title = "Inbox", command = "Settings", order = 45 }
oriel.view.override("tickets", function(ctx) return oriel.ui.text("tickets of " .. ctx.project) end)
`})
	s.lua().reload(false)
	s.frame()
	s.frame()
	if !strings.Contains(s.frameText(), "Tickets") {
		t.Fatalf("no Tickets under admin-ui:\n%s", s.frameText())
	}
	text := s.frameText()
	if r, i := strings.Index(text, "\nRequests\n"), strings.Index(text, "\nInbox\n"); i < r || r < 0 {
		t.Errorf("Inbox isn't with the session's entries:\n%s", text)
	}
	s.run("project.open", registry.Args{"project": "api"})
	s.frame()
	s.click(centre(s.access("Tickets").Bounds))
	if !slices.ContainsFunc(s.visibleRefs(), func(v layout.ViewRef) bool { return v.ID == "tickets" && v.Project == "api" }) {
		t.Fatalf("Tickets didn't open for api: %v", s.visibleRefs())
	}
	if !strings.Contains(s.frameText(), "tickets of api") {
		t.Errorf("the view doesn't know its project:\n%s", s.frameText())
	}
	s.writeConfig(t, map[string]string{"init.lua": ""})
	s.lua().reload(false)
	s.frame()
	names := []string{}
	for _, e := range registry.RailEntries(s.reg) {
		names = append(names, e.Name)
	}
	if slices.Contains(names, "tickets") || slices.Contains(names, "inbox") {
		t.Errorf("the entries outlived the config: %v", names)
	}
}

// SH-17: a file from another project opens in the one tab strip, led by its project's colour, its tooltip and
// breadcrumb naming the project; the sidebar stays where it was.
func TestSidebarTabsAcrossProjects(t *testing.T) {
	ps := sidebarFixture(t)
	s := sessionScene(t, core.KindWork, ps, mockupSize)
	s.run("editor.open", registry.Args{"path": filepath.Join(ps[0].Dir, "src/components/FilterBar.tsx")})
	s.run("editor.open", registry.Args{"path": filepath.Join(ps[1].Dir, "proto/usage.proto")})
	s.frame()
	if got := s.openProject(); got != "admin-ui" {
		t.Errorf("opening api's file moved the sidebar to %q", got)
	}
	var tabs []layout.ViewRef
	for _, v := range s.sh.layout.root.Shown() {
		tabs = append(tabs, v)
	}
	leaf := s.sh.layout.root.Find(tabs[0]).Leaf
	if len(leaf.Tabs) != 2 {
		t.Fatalf("one tab strip: %v", leaf.Tabs)
	}
	violet := tokenColour(s.ui.Theme(), "violet", render.Color{})
	if c, ok := s.sh.layout.panes.Lead(leaf.Tabs[1]); !ok || c != violet {
		t.Errorf("usage.proto's tab leads with %v %v, want api's violet", c, ok)
	}
	if tip := s.sh.layout.panes.Tip(leaf.Tabs[1]); !strings.Contains(tip, "api") || !strings.Contains(tip, "proto/usage.proto") {
		t.Errorf("tooltip %q", tip)
	}
	if !strings.Contains(s.frameText(), "api\n›\nproto\n›\nusage.proto") {
		t.Errorf("the breadcrumb doesn't start with the project:\n%s", s.frameText())
	}
}

// SH-17: clicking the open entry hides the dock; the form is the window's and comes back after a restart; a
// directory without a remote has no Pull request; an entry is dimmed until its view is registered (Debug), and FG-6's
// Pull request and Ship are lit.
func TestSidebarFormDockAndAvailability(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	state := t.TempDir()
	dir := fixtureRepo(t, t.TempDir(), "blog", "main", map[string]string{"a.md": "a\n"}, 0, false)
	r := startApp(t, state, dir, nil)
	run := func(id string, args registry.Args) {
		t.Helper()
		if err := r.do(func() error { return r.host.Run(context.Background(), id, args) }); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	run("sidebar.form", registry.Args{"form": "wide"})
	r.waitText("Explorer")
	r.clickText("Explorer")
	r.waitText("ALL")
	r.clickText("Explorer")
	r.waitFrame("the dock hidden", func(s string) bool { return !strings.Contains(s, "ALL") })
	if strings.Contains(r.waitText("Explorer"), "Pull request") {
		t.Error("a directory without a remote lists Pull request")
	}
	run("sidebar.form", registry.Args{"form": "narrow"})
	r.waitText("»")
	r.stop()
	r2 := startApp(t, state, dir, nil)
	r2.waitText("»")
	r2.stop()

	ps := sidebarFixture(t)
	s := sessionScene(t, core.KindWork, ps, sidebarSize)
	if s.access("Pull request").Disabled || s.access("Ship").Disabled {
		t.Error("Pull request and Ship are dimmed, though FG-6 registers their views")
	}
	if s.access("Debug").Disabled {
		t.Error("Debug is dimmed, though DB-1 registers its view")
	}
}

// SH-17: the user adds a project to the session: + in the sidebar's header picks a repo or a typed path, and the
// project is added and opened.
func TestSidebarAddsAProject(t *testing.T) {
	lw := newLocalHost(t)
	billing := filepath.Join(t.TempDir(), "billing")
	git(t, filepath.Dir(billing), "init", "-q", "-b", "main", billing)
	git(t, billing, "commit", "-q", "--allow-empty", "-m", "first")
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	err := r.do(func() error {
		return r.host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "rounding", "repos": []string{lw.repo}})
	})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, r.store, "the session in front", func(st core.State) bool { return st.Focus == "rounding" })
	r.waitFrame("the window on the session", func(s string) bool { return strings.Contains(s, "+") && !strings.Contains(s, "Open Directory") })
	r.clickAccess("Add a project")
	r.waitText("ADD A PROJECT")
	r.typeText(billing)
	r.waitText("Add " + billing)
	r.typeKeys("<CR>")
	st := waitState(t, r.store, "billing in the session", func(st core.State) bool {
		se, ok := st.Session("rounding")
		return ok && len(se.Projects()) == 2
	})
	se, _ := st.Session("rounding")
	if names := []string{se.Projects()[0].Name, se.Projects()[1].Name}; names[1] != "billing" {
		t.Fatalf("projects %v", names)
	}
	waitState(t, r.store, "billing open", func(st core.State) bool {
		se, _ := st.Session("rounding")
		return se.Snapshot().OpenProject() == "billing"
	})
	r.waitFrame("both projects' headers", func(s string) bool { return containsAll(s, "pricing", "billing", "⌃2") })
}
