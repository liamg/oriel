package registry_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
)

// ui.md 04: "A pasted ticket ID expands into a card": the first source by order that takes the key looks it up.
func TestTicketSources(t *testing.T) {
	r := registry.New()
	if _, ok := registry.TicketFor(r, "INF-2231"); ok {
		t.Error("a source with none registered")
	}
	r.Contribute(registry.TicketSource{Name: "any", Order: 50, Lookup: func(_ context.Context, key string) (registry.Ticket, error) {
		return registry.Ticket{Key: key, Title: "from any"}, nil
	}})
	r.Contribute(registry.TicketSource{Name: "linear", Title: "Linear", Order: 10,
		Match: func(key string) bool { return strings.HasPrefix(key, "INF-") },
		Lookup: func(_ context.Context, key string) (registry.Ticket, error) {
			if key == "INF-404" {
				return registry.Ticket{}, errors.New("linear has no ticket INF-404")
			}
			return registry.Ticket{Key: key, Title: "Usage API returns stale prices"}, nil
		}})
	s, ok := registry.TicketFor(r, " INF-2231 ")
	if !ok || s.Name != "linear" {
		t.Fatalf("INF-2231 went to %q, %v", s.Name, ok)
	}
	got, err := registry.LookupTicket(context.Background(), s, " INF-2231 ")
	if err != nil || got.Key != "INF-2231" || got.Source != "Linear" {
		t.Errorf("got %+v, %v", got, err)
	}
	if _, err := registry.LookupTicket(context.Background(), s, "INF-404"); err == nil {
		t.Error("a ticket the source hasn't")
	}
	if s, _ := registry.TicketFor(r, "ENG-1"); s.Name != "any" {
		t.Errorf("ENG-1 went to %q", s.Name)
	}
	if _, ok := registry.TicketFor(r, "  "); ok {
		t.Error("an empty key")
	}
}
