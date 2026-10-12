package core

import (
	"fmt"
	"slices"
)

func init() {
	RegisterEvent[SessionOpened]("SessionOpened")
	RegisterEvent[SessionStatusChanged]("SessionStatusChanged")
	RegisterEvent[SessionClosed]("SessionClosed")
}

// SessionOpened adds a session to the store.
type SessionOpened struct {
	ID     SessionID `json:"id"`
	Kind   Kind      `json:"kind"`
	Status Status    `json:"status"`
}

func (e SessionOpened) Scope() SessionID { return e.ID }

func (e SessionOpened) Apply(s *State) error {
	if s.session(e.ID) != nil {
		return fmt.Errorf("session %s is already open", e.ID)
	}
	s.Sessions = append(s.Sessions, SessionState{ID: e.ID, Kind: e.Kind, Status: e.Status})
	return nil
}

// SessionStatusChanged moves a session to a new status.
type SessionStatusChanged struct {
	ID     SessionID `json:"id"`
	Status Status    `json:"status"`
}

func (e SessionStatusChanged) Scope() SessionID { return e.ID }

func (e SessionStatusChanged) Apply(s *State) error {
	se := s.session(e.ID)
	if se == nil {
		return fmt.Errorf("no session %s", e.ID)
	}
	se.Status = e.Status
	return nil
}

// SessionClosed removes a session from the store.
type SessionClosed struct {
	ID SessionID `json:"id"`
}

func (e SessionClosed) Scope() SessionID { return e.ID }

func (e SessionClosed) Apply(s *State) error {
	if s.session(e.ID) == nil {
		return fmt.Errorf("no session %s", e.ID)
	}
	s.Sessions = slices.DeleteFunc(s.Sessions, func(se SessionState) bool { return se.ID == e.ID })
	return nil
}
