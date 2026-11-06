package core

import (
	"fmt"
	"maps"
	"slices"
)

func init() {
	RegisterEvent[ProjectOpened]("ProjectOpened")
	RegisterEvent[ProjectPanelOpened]("ProjectPanelOpened")
	RegisterEvent[ProjectColoured]("ProjectColoured")
	RegisterEvent[ProjectMoved]("ProjectMoved")
	RegisterEvent[ProjectFolded]("ProjectFolded")
}

// ProjectUI is how the window shows a session's projects: the one open (its panels folded away or not), the panel
// each had last, colours chosen for them and the order they're listed in. The app keeps it; a sync of the session
// leaves it as it is.
type ProjectUI struct {
	Open    string            `json:"open,omitempty"`
	Folded  bool              `json:"folded,omitempty"`  // the open project's panels are folded away
	Order   []string          `json:"order,omitempty"`   // project names, as they were moved; the rest follow
	Panels  map[string]string `json:"panels,omitempty"`  // project → the name of its last panel
	Colours map[string]string `json:"colours,omitempty"` // project → a palette colour, when not its own
}

// ProjectColours are the palette projects take colours from, in the session's order.
var ProjectColours = []string{"cyan", "violet", "green", "amber", "pink", "blue", "orange", "red"}

// OpenProject is the project the user works in: the one opened last, else the primary one.
func (s SessionState) OpenProject() string {
	if s.ProjectUI.Open != "" && slices.ContainsFunc(s.Projects, func(p Project) bool { return p.Name == s.ProjectUI.Open }) {
		return s.ProjectUI.Open
	}
	if p, ok := s.Primary(); ok {
		return p.Name
	}
	return ""
}

// ListedProjects are the session's projects in the order the window lists them: those moved, as they were moved,
// then the rest in the session's order.
func (s SessionState) ListedProjects() []Project {
	if len(s.ProjectUI.Order) == 0 {
		return s.Projects
	}
	out := make([]Project, 0, len(s.Projects))
	for _, name := range s.ProjectUI.Order {
		if i := slices.IndexFunc(s.Projects, func(p Project) bool { return p.Name == name }); i >= 0 {
			out = append(out, s.Projects[i])
		}
	}
	for _, p := range s.Projects {
		if !slices.Contains(s.ProjectUI.Order, p.Name) {
			out = append(out, p)
		}
	}
	return out
}

// ProjectColour is a project's colour: the one set for it, else the palette's by its place in the session.
func (s SessionState) ProjectColour(name string) string {
	if c := s.ProjectUI.Colours[name]; c != "" {
		return c
	}
	i := max(0, slices.IndexFunc(s.Projects, func(p Project) bool { return p.Name == name }))
	return ProjectColours[i%len(ProjectColours)]
}

func (s *State) project(id SessionID, name string) (*SessionState, error) {
	se, err := s.mustSession(id)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(se.Projects, func(p Project) bool { return p.Name == name }) {
		return nil, fmt.Errorf("session %s has no project %q", id, name)
	}
	return se, nil
}

// ProjectOpened makes a project the open one.
type ProjectOpened struct {
	ID      SessionID `json:"id"`
	Project string    `json:"project"`
}

func (e ProjectOpened) Scope() SessionID { return e.ID }

func (e ProjectOpened) Apply(s *State) error {
	se, err := s.project(e.ID, e.Project)
	if err != nil {
		return err
	}
	se.ProjectUI.Open, se.ProjectUI.Folded = e.Project, false
	return nil
}

// ProjectMoved moves a project to Index (0-based) of the order the window lists them in.
type ProjectMoved struct {
	ID      SessionID `json:"id"`
	Project string    `json:"project"`
	Index   int       `json:"index"`
}

func (e ProjectMoved) Scope() SessionID { return e.ID }

func (e ProjectMoved) Apply(s *State) error {
	se, err := s.project(e.ID, e.Project)
	if err != nil {
		return err
	}
	var names []string
	for _, p := range se.ListedProjects() {
		if p.Name != e.Project {
			names = append(names, p.Name)
		}
	}
	se.ProjectUI.Order = slices.Insert(names, max(0, min(e.Index, len(names))), e.Project)
	return nil
}

// ProjectFolded folds the open project's panels away in the sidebar, or unfolds them.
type ProjectFolded struct {
	ID     SessionID `json:"id"`
	Folded bool      `json:"folded"`
}

func (e ProjectFolded) Scope() SessionID { return e.ID }

func (e ProjectFolded) Apply(s *State) error {
	se, err := s.mustSession(e.ID)
	if err != nil {
		return err
	}
	se.ProjectUI.Folded = e.Folded
	return nil
}

// ProjectPanelOpened is a project's panel opened: the one it reopens on when it's opened again.
type ProjectPanelOpened struct {
	ID      SessionID `json:"id"`
	Project string    `json:"project"`
	Panel   string    `json:"panel"`
}

func (e ProjectPanelOpened) Scope() SessionID { return e.ID }

func (e ProjectPanelOpened) Apply(s *State) error {
	se, err := s.project(e.ID, e.Project)
	if err != nil {
		return err
	}
	ps := maps.Clone(se.ProjectUI.Panels)
	if ps == nil {
		ps = map[string]string{}
	}
	ps[e.Project] = e.Panel
	se.ProjectUI.Panels = ps
	return nil
}

// ProjectColoured gives a project a colour of its own; "" puts back the palette's.
type ProjectColoured struct {
	ID      SessionID `json:"id"`
	Project string    `json:"project"`
	Colour  string    `json:"colour"`
}

func (e ProjectColoured) Scope() SessionID { return e.ID }

func (e ProjectColoured) Apply(s *State) error {
	se, err := s.project(e.ID, e.Project)
	if err != nil {
		return err
	}
	cs := maps.Clone(se.ProjectUI.Colours)
	if cs == nil {
		cs = map[string]string{}
	}
	if e.Colour == "" {
		delete(cs, e.Project)
	} else {
		cs[e.Project] = e.Colour
	}
	se.ProjectUI.Colours = cs
	return nil
}
