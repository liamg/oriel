// Package eventstore is a core.Store that materialises state from the events applied to it.
package eventstore

import (
	"reflect"
	"slices"
	"sync"

	"github.com/liamg/oriel/core"
)

type Store struct {
	apply   sync.Mutex // one Apply at a time, so every subscriber sees events in the same order
	mu      sync.RWMutex
	state   core.State
	subs    map[int]sub
	nextSub int
}

type sub struct {
	filter core.Filter
	fn     func(core.Event)
}

func New() *Store {
	return &Store{state: core.State{}.Clone(), subs: map[int]sub{}}
}

// State is the state as it stands: Apply works on a copy, so what it gives out never changes.
func (s *Store) State() core.State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *Store) Focus() core.SessionID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Focus
}

func (s *Store) Sessions() []core.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.SessionList()
}

func (s *Store) Session(id core.SessionID) (core.Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Session(id)
}

// Apply applies ev to a copy of the state and keeps it only if ev applied cleanly. Then it calls every
// matching subscriber, in the order they subscribed, with the event's times in UTC. An event that leaves the state as it was is dropped, unless
// it's core.Logged. Subscribers must not call Apply themselves.
func (s *Store) Apply(ev core.Event) error {
	s.apply.Lock()
	defer s.apply.Unlock()

	ev = core.InUTC(ev)
	s.mu.RLock()
	next := s.state.Clone()
	s.mu.RUnlock()
	if err := ev.Apply(&next); err != nil {
		return err
	}
	was := s.State()
	same := false
	if c, ok := ev.(core.SelfCompared); ok {
		same = c.Unchanged(&was, &next)
	} else {
		same = unchanged(was, next)
	}
	if _, logged := ev.(core.Logged); !logged && same {
		return nil
	}
	next.Seq++
	next.Sessions, next.Notifications = slices.Clip(next.Sessions), slices.Clip(next.Notifications) // a reader's append copies
	next.Machine.SignedOut, next.Usage, next.Repos = slices.Clip(next.Machine.SignedOut), slices.Clip(next.Usage), slices.Clip(next.Repos)

	s.mu.Lock()
	s.state = next
	ids := make([]int, 0, len(s.subs))
	for id := range s.subs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var fns []func(core.Event)
	for _, id := range ids {
		if s.subs[id].filter.Match(ev) {
			fns = append(fns, s.subs[id].fn)
		}
	}
	s.mu.Unlock()

	for _, fn := range fns {
		fn(ev)
	}
	return nil
}

// unchanged is whether next is was: slices and maps it shares with was compare at once.
func unchanged(was, next core.State) bool { return reflect.DeepEqual(was, next) }

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

// SubscribeState subscribes fn and returns the state as it was at that moment, with nothing between the two:
// fn sees every event after the state it's given, and none before.
func (s *Store) SubscribeState(filter core.Filter, fn func(core.Event)) (core.State, func()) {
	s.apply.Lock()
	defer s.apply.Unlock()
	return s.State(), s.Subscribe(filter, fn)
}
