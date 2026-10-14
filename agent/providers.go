package agent

import (
	"slices"
	"sync"
)

// Providers is the set of providers a worker can start, by ID. A later one with the same ID shadows the
// earlier until it's taken away.
type Providers struct {
	mu   sync.RWMutex
	list []Provider
}

func (s *Providers) Register(p Provider) (unregister func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, p)
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if i := slices.Index(s.list, p); i >= 0 {
			s.list = slices.Delete(s.list, i, i+1)
		}
	}
}

func (s *Providers) Get(id string) (Provider, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range slices.Backward(s.list) {
		if p.ID() == id {
			return p, true
		}
	}
	return nil, false
}

// List is the winning provider for each ID, in the order they were first registered.
func (s *Providers) List() []Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Provider
	for _, p := range s.list {
		if w, ok := s.getLocked(p.ID()); ok && w == p {
			out = append(out, p)
		}
	}
	return out
}

func (s *Providers) getLocked(id string) (Provider, bool) {
	for _, p := range slices.Backward(s.list) {
		if p.ID() == id {
			return p, true
		}
	}
	return nil, false
}
