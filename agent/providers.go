package agent

import (
	"slices"
	"sync"
)

// Providers is the set of providers a worker can start, by ID: its own, registered here, then those its source
// adds (the app's registry) for IDs it has none of. A later one with the same ID shadows the earlier until it's
// taken away.
type Providers struct {
	mu     sync.RWMutex
	list   []Provider
	source func() []Provider
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

// SetSource sets where the providers past the registered ones come from, asked on every lookup; nil is none.
func (s *Providers) SetSource(src func() []Provider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.source = src
}

func (s *Providers) Get(id string) (Provider, bool) {
	own, src := s.both()
	return find(own, src, id)
}

func find(own, src []Provider, id string) (Provider, bool) {
	for _, l := range [][]Provider{own, src} {
		for _, p := range slices.Backward(l) {
			if p.ID() == id {
				return p, true
			}
		}
	}
	return nil, false
}

// List is the winning provider for each ID, in the order they were first registered, then the source's for the IDs
// left, in its order.
func (s *Providers) List() []Provider {
	own, src := s.both()
	var out []Provider
	seen := map[string]bool{}
	for _, l := range [][]Provider{own, src} {
		for _, p := range l {
			if !seen[p.ID()] {
				seen[p.ID()] = true
				w, _ := find(own, src, p.ID())
				out = append(out, w)
			}
		}
	}
	return out
}

// both are the registered providers and the source's.
func (s *Providers) both() (own, src []Provider) {
	s.mu.RLock()
	fn := s.source
	own = slices.Clone(s.list)
	s.mu.RUnlock()
	if fn != nil {
		src = fn()
	}
	return own, src
}
