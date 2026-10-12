package core

import "slices"

// State is the materialised result of every event applied so far. It encodes to the same JSON
// whenever the same events have been applied.
type State struct {
	Seq      uint64         `json:"seq"` // events applied
	Sessions []SessionState `json:"sessions"`
}

type SessionState struct {
	ID     SessionID `json:"id"`
	Kind   Kind      `json:"kind"`
	Status Status    `json:"status"`
}

func (s *State) session(id SessionID) *SessionState {
	if i := slices.IndexFunc(s.Sessions, func(se SessionState) bool { return se.ID == id }); i >= 0 {
		return &s.Sessions[i]
	}
	return nil
}

// Clone returns a copy that shares nothing mutable with s.
func (s State) Clone() State {
	s.Sessions = slices.Clone(s.Sessions)
	if s.Sessions == nil {
		s.Sessions = []SessionState{}
	}
	return s
}

// Session returns the session with id as a read-only Session.
func (s State) Session(id SessionID) (Session, bool) {
	if se := s.session(id); se != nil {
		return sessionView{*se}, true
	}
	return nil, false
}

// SessionList returns every session, in the order they opened.
func (s State) SessionList() []Session {
	out := make([]Session, len(s.Sessions))
	for i, se := range s.Sessions {
		out[i] = sessionView{se}
	}
	return out
}

type sessionView struct{ s SessionState }

func (v sessionView) ID() SessionID  { return v.s.ID }
func (v sessionView) Kind() Kind     { return v.s.Kind }
func (v sessionView) Status() Status { return v.s.Status }
