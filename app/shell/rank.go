package shell

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit/fuzzy"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
)

const (
	// rankFresh is how long a source's items are reused before they're asked for again.
	rankFresh = 500 * time.Millisecond
	// rankAway is how many items make a ranking go off the loop; fewer are ranked in the frame.
	rankAway = 2000
)

// rankings rank picker sources for the omnibox and the pickers. A source's items are asked for on the loop, which
// owns what they're made from, and kept until its Key changes; one without a Key, for rankFresh or for as long as
// keys move through them. Matching them against the query runs off the loop when there are many, the last ranking
// shown until the new one is done.
type rankings struct {
	h  plugin.Host
	mu sync.Mutex
	by map[string]*ranking
}

type ranking struct {
	items   []uireg.PickerItem
	at      time.Time // when items were asked for
	asked   string    // the query they were asked for with
	key     any       // the source's Key when they were
	version int       // counts the items' changes
	recheck bool      // a redraw is due when items go stale

	query   string // what g ranks
	limit   int
	ranked  int // the version g ranks
	g       group
	running context.CancelFunc // the ranking off the loop, of want
	want    [3]any             // query, limit and version it ranks
}

func newRankings(h plugin.Host) *rankings { return &rankings{h: h, by: map[string]*ranking{}} }

// reset drops every ranking and stops the ones running: a picker opens, or goes.
func (r *rankings) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, k := range r.by {
		if k.running != nil {
			k.running()
		}
	}
	r.by = map[string]*ranking{}
}

// pending says a source's ranking for the query shown is still being made.
func (r *rankings) pending(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.by[id]
	return ok && k.running != nil
}

// rank is s's results for q, at most limit: the latest ranking made, while a newer one is made off the loop. A live
// source's items are its results already, asked for each time. moving keeps the items it has.
func (r *rankings) rank(s uireg.PickerSource, q string, limit int, moving bool) (group, bool) {
	if s.Live {
		return rankSource(r.h, s, q, limit)
	}
	r.mu.Lock()
	k := r.by[s.ID]
	if k == nil {
		k = &ranking{ranked: -1}
		r.by[s.ID] = k
	}
	var key any
	if s.Key != nil {
		if err := registry.Call(func() error { key = s.Key(); return nil }); err != nil {
			r.mu.Unlock()
			r.h.TurnOff("picker", s.ID, err)
			return group{}, false
		}
	}
	stale := k.items == nil || key != k.key
	if s.Key == nil {
		stale = stale || k.asked != q || !moving && time.Since(k.at) >= rankFresh
	}
	r.mu.Unlock()
	if stale {
		items, ok := sourceItems(r.h, s, q)
		if !ok {
			return group{}, false
		}
		r.mu.Lock()
		k.refresh(items, q)
		k.key = key
		r.mu.Unlock()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.Key == nil && !stale && !k.recheck {
		k.recheck = true // what changed since the items were asked for shows when they go stale
		time.AfterFunc(time.Until(k.at.Add(rankFresh)), func() {
			r.mu.Lock()
			k.recheck = false
			r.mu.Unlock()
			r.h.Redraw()
		})
	}
	if k.ranked == k.version && k.query == q && k.limit == limit {
		return k.g, len(k.g.hits) > 0
	}
	if len(k.items) <= rankAway {
		k.g, _ = rankItems(context.Background(), s, k.items, q, limit)
		k.query, k.limit, k.ranked = q, limit, k.version
		return k.g, len(k.g.hits) > 0
	}
	if want := [3]any{q, limit, k.version}; k.running == nil || k.want != want {
		if k.running != nil {
			k.running()
		}
		ctx, cancel := context.WithCancel(context.Background())
		k.running, k.want = cancel, want
		items, version := k.items, k.version
		go func() {
			g, ok := rankItems(ctx, s, items, q, limit)
			r.mu.Lock()
			if ok && ctx.Err() == nil {
				k.g, k.query, k.limit, k.ranked, k.running = g, q, limit, version, nil
			}
			r.mu.Unlock()
			cancel()
			r.h.Redraw()
		}()
	}
	return k.g, len(k.g.hits) > 0
}

// refresh takes a source's items as asked for again; when they show the same, the ranking made of the old ones
// stands, with the new ones in it.
func (k *ranking) refresh(items []uireg.PickerItem, q string) {
	k.at, k.asked = time.Now(), q
	if len(items) > 0 && len(items) == len(k.items) && &items[0] == &k.items[0] {
		return
	}
	if !slices.EqualFunc(k.items, items, sameItem) {
		k.items = items
		k.version++
		return
	}
	k.items = items
	if k.ranked != k.version {
		return
	}
	for i := range k.g.hits {
		k.g.hits[i].item = items[k.g.hits[i].idx]
	}
}

// sameItem says two items show the same, whatever their Data.
func sameItem(a, b uireg.PickerItem) bool {
	a.Data, b.Data = nil, nil
	return a == b
}

// sourceItems is a source's items for q, the source turned off when it fails.
func sourceItems(h plugin.Host, s uireg.PickerSource, q string) ([]uireg.PickerItem, bool) {
	if s.Items == nil {
		return nil, false
	}
	var items []uireg.PickerItem
	if err := registry.Call(func() error { items = s.Items(context.Background(), q); return nil }); err != nil {
		h.TurnOff("picker", s.ID, err)
		return nil, false
	}
	return items, true
}

// rankItems ranks items against q: the fuzzy score plus the item's boost, best first, at most limit; a live
// source's items in their order. It reports false when ctx ends first.
func rankItems(ctx context.Context, s uireg.PickerSource, items []uireg.PickerItem, q string, limit int) (group, bool) {
	terms := fuzzy.Parse(q)
	var hits []hit
	for i, it := range items {
		if i%512 == 0 && ctx.Err() != nil {
			return group{}, false
		}
		if s.Live {
			hits = append(hits, hit{s, it, it.Boost, nil, i})
			continue
		}
		text := it.Label // the detail and description match too, as fzf matches the whole line; only the label's light
		for _, more := range []string{it.Detail, it.Desc} {
			if more != "" {
				text += "  " + more
			}
		}
		score, pos, ok := fuzzy.MatchQuery(terms, text)
		if !ok {
			continue
		}
		hits = append(hits, hit{s, it, score + it.Boost, pos, i})
	}
	if !s.Live {
		slices.SortStableFunc(hits, func(a, b hit) int { return b.score - a.score })
	}
	return group{src: s, hits: hits[:min(len(hits), limit)], total: len(hits)}, true
}
