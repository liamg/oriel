package kit

import (
	"path/filepath"
	"slices"
	"sync"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui/layout"
)

// Here is what the window is on, as its projects: the session in front's when the window is on it, or
// the directory it's open on as one project.
type Here struct {
	Session  *core.SessionState // nil for a directory
	Projects []core.Project
	Open     string // the open project's name
}

// ProjectsHere reads it from the host, as a plugin would.
func ProjectsHere(h plugin.Host) Here {
	var se *core.SessionState
	if id := h.State().Focus; id != "" {
		if s, ok := h.Session(id); ok {
			st := s.Snapshot()
			se = &st
		}
	}
	return ProjectsIn(h.Here().Project, se)
}

// ProjectsIn is the window's projects, on directory work with session se in front (nil for none).
func ProjectsIn(work string, se *core.SessionState) Here {
	if se != nil {
		st := *se
		on := work == "" && len(st.Projects) > 0
		for _, d := range append([]string{st.Dir}, DirsOf(st.Projects)...) {
			if d != "" && work != "" && Inside(work, d) {
				on = true
			}
		}
		if on && len(st.Projects) > 0 {
			return Here{Session: &st, Projects: st.ListedProjects(), Open: st.OpenProject()}
		}
	}
	if work == "" {
		return Here{}
	}
	p := core.Project{Name: filepath.Base(work), Dir: work, Primary: true}
	return Here{Projects: []core.Project{p}, Open: p.Name}
}

func DirsOf(ps []core.Project) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Dir
	}
	return out
}

// Inside reports whether path is dir or under it, as written.
func Inside(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && filepath.IsLocal(rel) || rel == "."
}

// Within is Inside, as written or with symbolic links resolved; never for an empty path or dir.
func Within(path, dir string) bool {
	if dir == "" || path == "" {
		return false
	}
	if Inside(path, dir) {
		return true
	}
	rp, err1 := filepath.EvalSymlinks(path)
	rd, err2 := filepath.EvalSymlinks(dir)
	return err1 == nil && err2 == nil && Inside(rp, rd)
}

// Links is Within with each path's symbolic links read once and kept, for code that asks again and again. It's safe
// to use from any goroutine. The zero Links is ready.
type Links struct {
	mu sync.Mutex
	m  map[string]string
}

// Within is kit.Within, its paths resolved once.
func (l *Links) Within(path, dir string) bool {
	if dir == "" || path == "" {
		return false
	}
	if Inside(path, dir) {
		return true
	}
	rp, ok1 := l.real(path)
	rd, ok2 := l.real(dir)
	return ok1 && ok2 && Inside(rp, rd)
}

func (l *Links) real(p string) (string, bool) {
	l.mu.Lock()
	r, ok := l.m[p]
	l.mu.Unlock()
	if ok {
		return r, r != ""
	}
	r, _ = filepath.EvalSymlinks(p)
	l.mu.Lock()
	if l.m == nil {
		l.m = map[string]string{}
	}
	l.m[p] = r
	l.mu.Unlock()
	return r, r != ""
}

// Holds says whether session se has dir: its own directory, or one of its projects'.
func Holds(se core.SessionState, dir string) bool {
	return slices.ContainsFunc(append([]string{se.Dir}, DirsOf(se.Projects)...), func(d string) bool { return d != "" && Inside(dir, d) })
}

// Many says the window has projects to choose between: views then carry the project they're for.
func (w Here) Many() bool { return len(w.Projects) > 1 }

// Folded says the open project's panels are folded away in the sidebar.
func (w Here) Folded() bool { return w.Session != nil && w.Session.ProjectUI.Folded }

// Project is a project by name.
func (w Here) Project(name string) (core.Project, bool) {
	i := slices.IndexFunc(w.Projects, func(p core.Project) bool { return p.Name == name })
	if i < 0 {
		return core.Project{}, false
	}
	return w.Projects[i], true
}

// Dir is the directory of a view's project: the named one's, or the open one's for "".
func (w Here) Dir(name string) string {
	if name == "" {
		name = w.Open
	}
	p, _ := w.Project(name)
	return p.Dir
}

// Of is the project a path is in, the innermost when they nest.
func (w Here) Of(path string) (core.Project, bool) {
	best := -1
	for i, p := range w.Projects {
		if p.Dir != "" && filepath.IsAbs(path) && Inside(path, p.Dir) && (best < 0 || len(p.Dir) > len(w.Projects[best].Dir)) {
			best = i
		}
	}
	if best < 0 {
		return core.Project{}, false
	}
	return w.Projects[best], true
}

// Scope is the project a view opened now is for: the open one when there are several, else none (the window's).
func (w Here) Scope() string {
	if w.Many() {
		return w.Open
	}
	return ""
}

// Colour is a project's colour in th.
func (w Here) Colour(th *theme.Resolved, name string) render.Color {
	c := core.ProjectColours[0]
	if w.Session != nil {
		c = w.Session.ProjectColour(name)
	}
	return TokenColour(th, c, th.C(theme.Accent))
}

// Showing says whether a view with id is on screen for project ("" for the window's own).
func Showing(h plugin.Host, id, project string) bool {
	for _, v := range h.Layout().Visible() {
		if v.ID == id && v.Project == project {
			return true
		}
	}
	return false
}

// ViewArgs are view.toggle's args for a project's panel in the left dock.
func ViewArgs(id, title string, size int) registry.Args {
	a := registry.Args{"id": id, "place": "dock", "edge": "left", "title": title, "alone": true}
	if size > 0 {
		a["size"] = size
	}
	return a
}

// ViewFile is the file a view instance shows, for one that's Located (an editor's buffer).
func ViewFile(h plugin.Host, v layout.ViewRef) string {
	view, ok := h.ViewByID(v.ID)
	if !ok {
		return ""
	}
	l, ok := uireg.As[registry.Located](view)
	if !ok {
		return ""
	}
	file := ""
	registry.Call(func() error { file, _ = l.Location(v.Instance); return nil })
	return file
}

// ViewProject is the project a view instance is of: the one it was opened for, else the one its file is in.
func ViewProject(h plugin.Host, w Here, v layout.ViewRef) (core.Project, bool) {
	if v.Project != "" {
		return w.Project(v.Project)
	}
	if file := ViewFile(h, v); file != "" {
		return w.Of(file)
	}
	return core.Project{}, false
}

// RepoAt is the git repo whose root is dir: the GitTrees' own, sharing their cache, when they've read it.
func RepoAt(r registry.Registry, dir string) gitx.Repo {
	if gt, ok := services.GitTreesOf(r); ok {
		if t := gt.Holding(dir); t.Status() != nil && filepath.Clean(t.Root()) == filepath.Clean(dir) {
			return t.Status().Repo()
		}
	}
	return gitx.Repo{Dir: dir}
}
