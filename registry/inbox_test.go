package registry_test

import (
	"testing"

	"github.com/liamg/oriel/registry"
)

// SH-9: the inbox gathers every source's cards, by source, each keyed by its source; a source that panics gives
// none, and a disposed one goes.
func TestInboxCards(t *testing.T) {
	r := registry.New()
	bus := r.Contribute(registry.InboxSource{Name: "bus", Cards: func() []registry.InboxCard {
		return []registry.InboxCard{{ID: "q1", Type: "question"}, {ID: "r1", Type: "review-request"}}
	}})
	r.Contribute(registry.InboxSource{Name: "asks", Cards: func() []registry.InboxCard { return []registry.InboxCard{{ID: "p1"}} }})
	r.Contribute(registry.InboxSource{Name: "boom", Cards: func() []registry.InboxCard { panic("boom") }})
	var keys []string
	for _, c := range registry.InboxCards(r) {
		keys = append(keys, c.Key())
	}
	if want := []string{"asks/p1", "bus/q1", "bus/r1"}; !equal(keys, want) {
		t.Errorf("cards %v, want %v", keys, want)
	}
	bus.Dispose()
	if cs := registry.InboxCards(r); len(cs) != 1 || cs[0].Key() != "asks/p1" {
		t.Errorf("after the bus went: %+v", cs)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
