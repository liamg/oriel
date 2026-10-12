// Package memstore is an in-memory core.Store.
package memstore

import (
	"fmt"
	"slices"
	"sync"

	"github.com/liamg/oriel/core"
)

type Store struct {
	mu       sync.Mutex
	sessions []session
	subs     map[int]sub
	nextSub  int
}

type sub struct {
	filter core.Filter
	fn     func(core.Event)
}

type session struct {
	id     core.SessionID
	kind   core.Kind
	status core.Status
}

func (s session) ID() core.SessionID  { return s.id }
func (s session) Kind() core.Kind     { return s.kind }
func (s session) Status() core.Status { return s.status }

func New() *Store {
	return &Store{subs: map[int]sub{}}
}

func (s *Store) Sessions() []core.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]core.Session, len(s.sessions))
	for i, se := range s.sessions {
		out[i] = se
	}
	return out
}

func (s *Store) Session(id core.SessionID) (core.Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.index(id); i >= 0 {
		return s.sessions[i], true
	}
	return nil, false
}

func (s *Store) index(id core.SessionID) int {
	return slices.IndexFunc(s.sessions, func(se session) bool { return se.id == id })
}

// Apply changes state, then calls every matching subscriber in the order they subscribed.
func (s *Store) Apply(ev core.Event) error {
	s.mu.Lock()
	err := s.apply(ev)
	var fns []func(core.Event)
	if err == nil {
		ids := make([]int, 0, len(s.subs))
		for id := range s.subs {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			if s.subs[id].filter.Match(ev) {
				fns = append(fns, s.subs[id].fn)
			}
		}
	}
	s.mu.Unlock()
	for _, fn := range fns {
		fn(ev)
	}
	return err
}

func (s *Store) apply(ev core.Event) error {
	switch e := ev.(type) {
	case core.SessionOpened:
		if s.index(e.ID) >= 0 {
			return fmt.Errorf("session %s is already open", e.ID)
		}
		s.sessions = append(s.sessions, session{id: e.ID, kind: e.Kind, status: e.Status})
	case core.SessionStatusChanged:
		i := s.index(e.ID)
		if i < 0 {
			return fmt.Errorf("no session %s", e.ID)
		}
		s.sessions[i].status = e.Status
	}
	return nil
}

func (s *Store) Subscribe(filter core.Filter, fn func(core.Event)) (cancel func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSub
	s.nextSub++
	s.subs[id] = sub{filter, fn}
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.subs, id)
	}
}
