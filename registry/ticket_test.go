package registry_test

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"testing"

	"github.com/liamg/oriel/registry"
)

// A pasted ticket ID expands into a card: the first tracker by order whose keys take it looks it up.
func TestTicketTrackers(t *testing.T) {
	r := registry.New()
	if _, ok := registry.TrackerFor(r, "ENG-2231"); ok {
		t.Error("a tracker with none registered")
	}
	r.Contribute(registry.TicketTracker{Name: "any", Order: 50, Lookup: func(_ context.Context, key string) (registry.Ticket, error) {
		return registry.Ticket{Key: key, Title: "from any"}, nil
	}})
	r.Contribute(registry.TicketTracker{Name: "linear", Title: "Linear", Order: 10, Keys: regexp.MustCompile(`^ENG-\d+$`),
		Lookup: func(_ context.Context, key string) (registry.Ticket, error) {
			if key == "ENG-404" {
				return registry.Ticket{}, errors.New("linear has no ticket ENG-404")
			}
			return registry.Ticket{Key: key, Title: "Search API returns stale results"}, nil
		},
		Assigned: func(context.Context) ([]registry.Ticket, error) { return []registry.Ticket{{Key: "ENG-1"}}, nil }})
	if got := registry.TicketTrackers(r); len(got) != 2 || got[0].Name != "linear" {
		t.Fatalf("trackers %+v", got)
	}
	tr, ok := registry.TrackerFor(r, " ENG-2231 ")
	if !ok || tr.Name != "linear" {
		t.Fatalf("ENG-2231 went to %q, %v", tr.Name, ok)
	}
	got, err := registry.LookupTicket(context.Background(), tr, " ENG-2231 ")
	if err != nil || got.Key != "ENG-2231" || got.Tracker != "Linear" {
		t.Errorf("got %+v, %v", got, err)
	}
	if _, err := registry.LookupTicket(context.Background(), tr, "ENG-404"); err == nil {
		t.Error("a ticket the tracker hasn't")
	}
	if mine, err := registry.AssignedTickets(context.Background(), tr); err != nil || len(mine) != 1 || mine[0].Tracker != "Linear" {
		t.Errorf("assigned %+v, %v", mine, err)
	}
	if tr, _ := registry.TrackerFor(r, "OPS-1"); tr.Name != "any" {
		t.Errorf("OPS-1 went to %q", tr.Name)
	}
	if _, ok := registry.TrackerFor(r, "  "); ok {
		t.Error("an empty key")
	}
	any, _ := registry.TrackerFor(r, "OPS-1")
	if mine, err := registry.AssignedTickets(context.Background(), any); mine != nil || err != nil {
		t.Errorf("a tracker that can't list: %v %v", mine, err)
	}
}

// crit's ordering (internal/linear: sortIssues), as its tests have it, on any tracker's tickets.
func TestSortTickets(t *testing.T) {
	pr := &registry.TicketPR{Number: 1}
	ts := []registry.Ticket{
		{Key: "FIX-10", Priority: 3},
		{Key: "ENG-2", Priority: 0},
		{Key: "FIX-9", Priority: 3, PR: pr},
		{Key: "FIX-1", Priority: 1},
		{Key: "ENG-7", Priority: 2, Stage: registry.TicketStarted},
		{Key: "FIX-4", Priority: 4, Stage: registry.TicketInReview},
		{Key: "ENG-3", Priority: 2},
		{Key: "ENG-5", Priority: 2, Stage: registry.TicketStarted, Blocked: true},
	}
	registry.SortTickets(ts)
	var got []string
	for _, tk := range ts {
		got = append(got, tk.Key)
	}
	want := []string{"FIX-1", "ENG-7", "ENG-3", "ENG-5", "FIX-9", "FIX-10", "FIX-4", "ENG-2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	if registry.CompareTicketKeys("FIX-9", "FIX-10") >= 0 || registry.CompareTicketKeys("ENG-1", "FIX-1") >= 0 ||
		registry.CompareTicketKeys("NOPE", "FIX-1") <= 0 {
		t.Error("ticket keys out of order")
	}
}
