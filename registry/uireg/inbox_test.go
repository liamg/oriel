package uireg_test

import (
	"testing"

	"github.com/liamg/oriel/registry/uireg"
)

// The inbox gathers every source's cards, by source, each keyed by its source; a source that panics gives
// none, and a disposed one goes.
func TestInboxCards(t *testing.T) {
	r := uireg.New()
	bus := r.Contribute(uireg.InboxSource{Name: "bus", Cards: func() []uireg.InboxCard {
		return []uireg.InboxCard{{ID: "q1", Type: "question"}, {ID: "r1", Type: "review-request"}}
	}})
	r.Contribute(uireg.InboxSource{Name: "asks", Cards: func() []uireg.InboxCard { return []uireg.InboxCard{{ID: "p1"}} }})
	r.Contribute(uireg.InboxSource{Name: "boom", Cards: func() []uireg.InboxCard { panic("boom") }})
	var keys []string
	for _, c := range uireg.InboxCards(r) {
		keys = append(keys, c.Key())
	}
	if want := []string{"asks/p1", "bus/q1", "bus/r1"}; !equal(keys, want) {
		t.Errorf("cards %v, want %v", keys, want)
	}
	bus.Dispose()
	if cs := uireg.InboxCards(r); len(cs) != 1 || cs[0].Key() != "asks/p1" {
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
