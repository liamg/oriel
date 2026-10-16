package syntax

import "sync"

// maxIdle is how many idle parsers a language keeps for Borrow.
const maxIdle = 4

var idle = struct {
	sync.Mutex
	by map[*Language][]*Parser
}{by: map[*Language][]*Parser{}}

// Borrow is a parser for l, idle since a Release or new: one-off work (a diff's highlights, a file's outline) reuses
// one, with its queries already compiled, rather than starting and compiling its own. Parse before use.
func Borrow(l *Language) (*Parser, error) {
	idle.Lock()
	if ps := idle.by[l]; len(ps) > 0 {
		p := ps[len(ps)-1]
		idle.by[l] = ps[:len(ps)-1]
		idle.Unlock()
		return p, nil
	}
	idle.Unlock()
	return NewParser(l)
}

// Release gives a borrowed parser back, its trees and text dropped, or closes it when its language has enough idle.
func (p *Parser) Release() {
	for _, t := range []*uint32{&p.tree, &p.prev} {
		if *t != 0 {
			p.m.call("tree_delete", uint64(*t))
			*t = 0
		}
	}
	p.src, p.version, p.synced, p.whole = nil, 0, false, false
	idle.Lock()
	if ps := idle.by[p.lang]; len(ps) < maxIdle {
		idle.by[p.lang] = append(ps, p)
		idle.Unlock()
		return
	}
	idle.Unlock()
	p.Close()
}
