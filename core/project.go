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
}

// ProjectUI is how the window shows a session's projects (ui.md 01h): the one open, the panel each had last, and
// colours chosen for them. The app keeps it; a sync of the session leaves it as it is.
type ProjectUI struct {
	Open    string            `json:"open,omitempty"`
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
	se.ProjectUI.Open = e.Project
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
