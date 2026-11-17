package syntax

import "sync"

const maxIdle = 4 // idle parsers a language keeps for Borrow

// pool is a set's idle parsers, by language. A closed one keeps none.
type pool struct {
	mu     sync.Mutex
	by     map[*Language][]*Parser
	closed bool
}

// Close closes the set's idle parsers; from then on a parser of its languages given back closes. A set made again
// (a plugin adds a language) closes the one it replaces.
func (s *Set) Close() {
	s.idle.mu.Lock()
	var stale []*Parser
	for _, ps := range s.idle.by {
		stale = append(stale, ps...)
	}
	s.idle.by, s.idle.closed = nil, true
	s.idle.mu.Unlock()
	for _, p := range stale {
		p.Close()
	}
}

// Borrow is a parser for l, idle since a Release or new: one-off work (a diff's highlights, a file's outline) reuses
// one, with its queries already compiled, rather than starting and compiling its own. Parse before use. The idle
// parsers are l's set's.
func Borrow(l *Language) (*Parser, error) {
	if s := l.set; s != nil {
		s.idle.mu.Lock()
		if ps := s.idle.by[l]; len(ps) > 0 {
			p := ps[len(ps)-1]
			if ps = ps[:len(ps)-1]; len(ps) == 0 {
				delete(s.idle.by, l)
			} else {
				s.idle.by[l] = ps
			}
			s.idle.mu.Unlock()
			return p, nil
		}
		s.idle.mu.Unlock()
	}
	return NewParser(l)
}

// Release gives a borrowed parser back to its language's set, its trees and text dropped, or closes it: when the
// language has enough idle, has no set, or its set is closed.
func (p *Parser) Release() {
	for _, t := range []*uint32{&p.tree, &p.prev} {
		if *t != 0 {
			p.m.call("tree_delete", uint64(*t))
			*t = 0
		}
	}
	p.src, p.version, p.synced, p.whole = nil, 0, false, false
	s := p.lang.set
	if s == nil {
		p.Close()
		return
	}
	s.idle.mu.Lock()
	if ps := s.idle.by[p.lang]; !s.idle.closed && len(ps) < maxIdle {
		if s.idle.by == nil {
			s.idle.by = map[*Language][]*Parser{}
		}
		s.idle.by[p.lang] = append(ps, p)
		s.idle.mu.Unlock()
		return
	}
	s.idle.mu.Unlock()
	p.Close()
}
