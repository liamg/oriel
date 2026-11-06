package ship

import (
	"context"
	"slices"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
)

// keepNewer is a whole read numbered seq, ships, with the entry of each project read on its own since kept in its
// place; the rest are noted as from seq. r's lock is held.
func (r *shipReport) keepNewer(ships []shipRead, seq int) []shipRead {
	if r.from == nil {
		r.from = map[string]int{}
	}
	for i, s := range ships {
		if r.from[s.Project] > seq {
			if j := slices.IndexFunc(r.ships, func(o shipRead) bool { return o.Project == s.Project }); j >= 0 {
				ships[i] = r.ships[j]
				continue
			}
		}
		r.from[s.Project] = seq
	}
	return ships
}

// readProject reads one project of a session again, on this goroutine: its git changed, so the others' readings
// stand. A session not read whole yet is read whole, once; a whole read running reads it, and once more after if
// the change wasn't the watch's first look (which the whole read covers).
func (x *Ship) readProject(id core.SessionID, project string, first bool) {
	x.mu.Lock()
	r := x.reports[id]
	switch {
	case r != nil && r.fixed:
		x.mu.Unlock()
		return
	case r != nil && r.loading:
		r.again = r.again || !first
		x.mu.Unlock()
		return
	case r == nil || !slices.ContainsFunc(r.ships, func(s shipRead) bool { return s.Project == project }):
		r, n, seq := x.reading(id)
		x.mu.Unlock()
		x.readAs(id, r, n, seq, false)
		return
	case first:
		x.mu.Unlock()
		return
	}
	x.mu.Unlock()
	x.readOne(id, project, false)
}

// readOne reads one project of a session again, on this goroutine, its PRs from the code host when fresh: a step
// on it, or a change to its git, leaves the others' readings standing. A session or project not read yet is read
// whole.
func (x *Ship) readOne(id core.SessionID, project string, fresh bool) {
	x.mu.Lock()
	r := x.reports[id]
	if r == nil || r.fixed || !slices.ContainsFunc(r.ships, func(s shipRead) bool { return s.Project == project }) {
		if r != nil && r.fixed {
			x.mu.Unlock()
			return
		}
		r, n, seq := x.reading(id)
		x.mu.Unlock()
		x.readAs(id, r, n, seq, fresh)
		return
	}
	r.seq++
	seq := r.seq
	x.mu.Unlock()
	c, err := x.client(id)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(x.ctx, 2*time.Minute)
	read, err := c.ShipRead(ctx, host.ShipReq{Session: id, Projects: []string{project}})
	var ships []shipRead
	if err == nil && len(read) == 1 {
		ships = x.withPRs(ctx, id, read, fresh)
	}
	cancel()
	if len(ships) != 1 {
		x.refresh(id, fresh)
		return
	}
	x.mu.Lock()
	i := slices.IndexFunc(r.ships, func(s shipRead) bool { return s.Project == project })
	if r.fixed || i < 0 || r.from[project] > seq {
		x.mu.Unlock()
		return
	}
	if r.from == nil {
		r.from = map[string]int{}
	}
	r.ships = slices.Clone(r.ships)
	r.ships[i], r.from[project] = ships[0], seq
	seen, merged, asks := x.newPRs(id, r.ships)
	x.mu.Unlock()
	x.fire(id, seen, merged, asks)
	x.h.Redraw()
}
