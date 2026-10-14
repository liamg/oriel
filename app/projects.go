package app

import (
	"path/filepath"
	"slices"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui/layout"
)

// here is what the window is on, as its projects (ui.md 01h): the session in front's when the window is on it, or
// the directory it's open on as one project.
type here struct {
	session  *core.SessionState // nil for a directory
	projects []core.Project
	open     string // the open project's name
}

// projectsHere reads it from the host, as a plugin would.
func projectsHere(h ext.Host) here {
	work := ""
	if at := h.Here(); at.Project != "" {
		work = at.Project
	}
	if id := h.State().Focus; id != "" {
		if se, ok := h.Session(id); ok {
			st := se.Snapshot()
			on := work == "" && len(st.Projects) > 0
			for _, d := range append([]string{st.Dir}, dirsOf(st.Projects)...) {
				if d != "" && work != "" && inside(work, d) {
					on = true
				}
			}
			if on && len(st.Projects) > 0 {
				return here{session: &st, projects: st.Projects, open: st.OpenProject()}
			}
		}
	}
	if work == "" {
		return here{}
	}
	p := core.Project{Name: filepath.Base(work), Dir: work, Primary: true}
	return here{projects: []core.Project{p}, open: p.Name}
}

func dirsOf(ps []core.Project) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Dir
	}
	return out
}

// inside reports whether path is dir or under it, as written.
func inside(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && filepath.IsLocal(rel) || rel == "."
}

// many says the window has projects to choose between: views then carry the project they're for.
func (w here) many() bool { return len(w.projects) > 1 }

// project is a project by name.
func (w here) project(name string) (core.Project, bool) {
	i := slices.IndexFunc(w.projects, func(p core.Project) bool { return p.Name == name })
	if i < 0 {
		return core.Project{}, false
	}
	return w.projects[i], true
}

// dir is the directory of a view's project: the named one's, or the open one's for "".
func (w here) dir(name string) string {
	if name == "" {
		name = w.open
	}
	p, _ := w.project(name)
	return p.Dir
}

// of is the project a path is in, the innermost when they nest.
func (w here) of(path string) (core.Project, bool) {
	best := -1
	for i, p := range w.projects {
		if p.Dir != "" && filepath.IsAbs(path) && inside(path, p.Dir) && (best < 0 || len(p.Dir) > len(w.projects[best].Dir)) {
			best = i
		}
	}
	if best < 0 {
		return core.Project{}, false
	}
	return w.projects[best], true
}

// scope is the project a view opened now is for: the open one when there are several, else none (the window's).
func (w here) scope() string {
	if w.many() {
		return w.open
	}
	return ""
}

// colour is a project's colour in th.
func (w here) colour(th *theme.Resolved, name string) render.Color {
	c := core.ProjectColours[0]
	if w.session != nil {
		c = w.session.ProjectColour(name)
	}
	return tokenColour(th, c, th.C(theme.Accent))
}

// showing says whether a view with id is on screen for project ("" for the window's own).
func showing(h ext.Host, id, project string) bool {
	for _, v := range h.Layout().Visible() {
		if v.ID == id && v.Project == project {
			return true
		}
	}
	return false
}

// viewArgs are view.toggle's args for a project's panel in the left dock.
func viewArgs(id, title string, size int) registry.Args {
	a := registry.Args{"id": id, "place": "dock", "edge": "left", "title": title}
	if size > 0 {
		a["size"] = size
	}
	return a
}

// viewFile is the file a view instance shows, for one that's Located (an editor's buffer).
func viewFile(h ext.Host, v layout.ViewRef) string {
	view, ok := h.ViewByID(v.ID)
	if !ok {
		return ""
	}
	l, ok := registry.As[registry.Located](view)
	if !ok {
		return ""
	}
	file := ""
	registry.Call(func() error { file, _ = l.Location(v.Instance); return nil })
	return file
}

// viewProject is the project a view instance is of: the one it was opened for, else the one its file is in.
func viewProject(h ext.Host, w here, v layout.ViewRef) (core.Project, bool) {
	if v.Project != "" {
		return w.project(v.Project)
	}
	if file := viewFile(h, v); file != "" {
		return w.of(file)
	}
	return core.Project{}, false
}
