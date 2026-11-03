package apptest

import (
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// OpenFile opens name in the editor and answers where the window now is: its session, project, buffer and file.
func (s *Scene) OpenFile(name string) registry.At {
	s.T.Helper()
	s.Run("editor.open", registry.Args{"path": name})
	return s.Host().Here()
}

// Option is name's value at at, and where it was set.
func (s *Scene) Option(name string, at registry.At) (any, string) {
	s.T.Helper()
	v, _, err := s.Registry().Options().Get(name, at)
	if err != nil {
		s.T.Fatal(err)
	}
	return v, s.Registry().Options().Source(name, at)
}

// Action is n's action titled title.
func Action(n core.Notification, title string) (core.Action, bool) {
	for _, a := range n.Actions {
		if a.Title == title {
			return a, true
		}
	}
	return core.Action{}, false
}
