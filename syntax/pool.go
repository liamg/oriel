package syntax

import (
	"slices"
	"sync"
)

const (
	maxIdle      = 4  // idle parsers a language keeps for Borrow
	maxIdleLangs = 16 // languages that keep any: past it, the one released longest ago closes its
)

var idle = struct {
	sync.Mutex
	by    map[*Language][]*Parser
	order []*Language // languages with idle parsers, released longest ago first
}{by: map[*Language][]*Parser{}}

// Borrow is a parser for l, idle since a Release or new: one-off work (a diff's highlights, a file's outline) reuses
// one, with its queries already compiled, rather than starting and compiling its own. Parse before use.
func Borrow(l *Language) (*Parser, error) {
	idle.Lock()
	if ps := idle.by[l]; len(ps) > 0 {
		p := ps[len(ps)-1]
		if ps = ps[:len(ps)-1]; len(ps) == 0 {
			delete(idle.by, l)
			idle.order = slices.DeleteFunc(idle.order, func(x *Language) bool { return x == l })
		} else {
			idle.by[l] = ps
		}
		idle.Unlock()
		return p, nil
	}
	idle.Unlock()
	return NewParser(l)
}

// Release gives a borrowed parser back, its trees and text dropped, or closes it when its language has enough idle.
// A language set made again (a plugin adds one) leaves the old languages' parsers to close as newer ones are released.
func (p *Parser) Release() {
	for _, t := range []*uint32{&p.tree, &p.prev} {
		if *t != 0 {
			p.m.call("tree_delete", uint64(*t))
			*t = 0
		}
	}
	p.src, p.version, p.synced, p.whole = nil, 0, false, false
	var stale []*Parser
	idle.Lock()
	ps := idle.by[p.lang]
	if len(ps) >= maxIdle {
		idle.Unlock()
		p.Close()
		return
	}
	idle.by[p.lang] = append(ps, p)
	idle.order = append(slices.DeleteFunc(idle.order, func(x *Language) bool { return x == p.lang }), p.lang)
	for len(idle.order) > maxIdleLangs {
		old := idle.order[0]
		idle.order = idle.order[1:]
		stale = append(stale, idle.by[old]...)
		delete(idle.by, old)
	}
	idle.Unlock()
	for _, s := range stale {
		s.Close()
	}
}
