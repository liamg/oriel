package registry

import (
	"cmp"
	"context"
	"slices"
	"strings"
)

// Ticket is a ticket as its tracker tells it: the launcher's card, and what goes to the agent with the prompt.
type Ticket struct {
	Key       string
	Title     string
	URL       string
	State     string   // "In Progress"
	StateTone string   // the state chip's theme token; accent when ""
	Summary   string   // the description's start
	Facts     []string // "2 attachments", "linked PR #4398", "4 comments"
	Context   string   // markdown: the ticket, its comments and attachments, handed to the agent with the prompt
	Source    string   // the source's Title: "Linear"
}

// TicketSource looks up tickets by key for whatever names one: the launcher's ticket field. A tracker's plugin
// registers one (Linear's); the first by Order whose Match takes the key answers.
type TicketSource struct {
	Name   string
	Title  string // "Linear"
	Order  int
	Match  func(key string) bool // nil takes every key
	Lookup func(ctx context.Context, key string) (Ticket, error)
	Source string
}

func (TicketSource) ContributionKind() string   { return "ticket.source" }
func (s TicketSource) ContributionName() string { return s.Name }

// TicketSources are the ticket sources by Order, then name.
func TicketSources(r Registry) []TicketSource {
	var out []TicketSource
	for _, c := range r.Contributions("ticket.source") {
		out = append(out, c.(TicketSource))
	}
	slices.SortFunc(out, func(a, b TicketSource) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}

// TicketFor is the first source that takes key; ok is false when none does. Call it on the loop: a source's Match
// may be Lua's.
func TicketFor(r Registry, key string) (TicketSource, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return TicketSource{}, false
	}
	for _, s := range TicketSources(r) {
		takes := s.Match == nil
		if !takes {
			Call(func() error { takes = s.Match(key); return nil })
		}
		if takes && s.Lookup != nil {
			return s, true
		}
	}
	return TicketSource{}, false
}

// LookupTicket asks s for key's ticket, its Source filled in.
func LookupTicket(ctx context.Context, s TicketSource, key string) (Ticket, error) {
	var t Ticket
	err := Call(func() (err error) { t, err = s.Lookup(ctx, strings.TrimSpace(key)); return err })
	if err != nil {
		return Ticket{}, err
	}
	t.Source = cmp.Or(t.Source, s.Title, s.Name)
	return t, nil
}
