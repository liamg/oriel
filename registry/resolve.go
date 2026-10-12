package registry

import (
	"slices"
	"time"
)

// Resolver turns key presses into mappings, waiting for the rest of a sequence the way vim does
// (map.txt *map-typing*). It isn't safe for concurrent use; one runs per input loop.
type Resolver struct {
	reg      Registry
	modes    []string
	pending  []string
	cand     *Keymap // the longest complete mapping in pending so far
	candLen  int
	deadline time.Time
}

// Action is one thing to do: run Keymap, or pass Keys on unmapped.
type Action struct {
	Keymap *Keymap
	Keys   []string
}

func NewResolver(r Registry) *Resolver { return &Resolver{reg: r} }

// Feed takes one key, in notation, typed in modes (most specific first).
func (r *Resolver) Feed(modes []string, key string, now time.Time) []Action {
	r.modes = modes
	return r.feed(nil, []string{key}, now)
}

// Timeout gives up waiting once now reaches the deadline: the longest complete mapping runs, or else
// the first pending key passes on unmapped. Keys after either are fed again.
func (r *Resolver) Timeout(now time.Time) []Action {
	if len(r.pending) == 0 || r.deadline.IsZero() || now.Before(r.deadline) {
		return nil
	}
	out, rest := r.flush(nil, nil)
	return r.feed(out, rest, now)
}

// Deadline is when Timeout should be called, or zero when nothing is waiting on a timer.
func (r *Resolver) Deadline() time.Time { return r.deadline }

// Pending is the keys typed so far towards a longer mapping.
func (r *Resolver) Pending() []string { return slices.Clone(r.pending) }

func (r *Resolver) feed(out []Action, keys []string, now time.Time) []Action {
	for len(keys) > 0 {
		r.pending = append(r.pending, keys[0])
		keys = keys[1:]
		exact, more := r.reg.Lookup(r.modes, r.pending)
		if exact != nil {
			r.cand, r.candLen = exact, len(r.pending)
		}
		if !more {
			out, keys = r.flush(out, keys)
		}
	}
	r.deadline = time.Time{}
	if len(r.pending) > 0 {
		if on, _, _ := r.reg.Options().Get("keymap.timeout", At{}); on == true {
			d, _, _ := r.reg.Options().Get("keymap.timeoutlen", At{})
			r.deadline = now.Add(d.(time.Duration))
		}
	}
	return out
}

// flush settles pending and returns the keys still to feed.
func (r *Resolver) flush(out []Action, keys []string) ([]Action, []string) {
	var rest []string
	if r.cand != nil {
		out = append(out, Action{Keymap: r.cand})
		rest = r.pending[r.candLen:]
	} else {
		out = append(out, Action{Keys: r.pending[:1]})
		rest = r.pending[1:]
	}
	rest = append(slices.Clone(rest), keys...)
	r.pending, r.cand, r.candLen = nil, nil, 0
	return out, rest
}
