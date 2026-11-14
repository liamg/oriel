package apptest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
)

// SidebarSize is a slice of the window as 42 draws each case.
var SidebarSize = render.Point{X: 760, Y: 420}

// FixtureRepo is a clone of a bare origin with files committed on main, then on branch (tracking origin/main) the
// commits ahead, each touching a file; remote false drops the origin.
func FixtureRepo(t *testing.T, root, name, branch string, files map[string]string, ahead int, remote bool) string {
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

// SidebarProjects are three projects: admin-ui two ahead on eng-2310 with four files changed, api on
// rate-limits, web on main, both clean.
func SidebarProjects(t *testing.T) []core.Project {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	root := t.TempDir()
	admin := FixtureRepo(t, root, "admin-ui", "eng-2310", map[string]string{
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
	api := FixtureRepo(t, root, "api", "rate-limits", map[string]string{
		"proto/usage.proto":  "package usage.v1;\n\nmessage Filter {\n  string field = 1;\n  string needle = 2;\n}\n",
		"limiter/limiter.go": "package limiter\n",
	}, 0, true)
	web := FixtureRepo(t, root, "web", "main", map[string]string{"index.html": "<html></html>\n"}, 0, true)
	return []core.Project{{Name: "admin-ui", Dir: admin, Branch: "eng-2310", Primary: true}, {Name: "api", Dir: api, Branch: "rate-limits"},
		{Name: "web", Dir: web, Branch: "main"}}
}

// SessionScene is the window on a session in front with projects, the first one's directory open, at size.
func SessionScene(t *testing.T, kind core.Kind, projects []core.Project, size render.Point) *Scene {
	t.Helper()
	s := New(t, Options{WorkDir: projects[0].Dir, Size: size})
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

// OpenProject is the project the scene's session has open.
func OpenProject(s *Scene) string {
	se, _ := s.Store().State().Session("eng-2310")
	return se.Snapshot().OpenProject()
}
