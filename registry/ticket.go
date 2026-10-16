package registry

import (
	"cmp"
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A ticket's stage: where it is in the work, whatever its tracker calls the state. It colours the state and orders
// the list.
const (
	TicketTodo     = "todo" // not started: triage, backlog, to do
	TicketStarted  = "started"
	TicketInReview = "review"
)

// Ticket is a ticket as its tracker tells it: a row of the tickets list, the launcher's card, and what goes to the
// agent with the prompt. Its JSON is the same shape Lua and a plugin's process give it in.
type Ticket struct {
	Key           string    `json:"key"`
	Title         string    `json:"title"`
	URL           string    `json:"url,omitempty"`
	State         string    `json:"state,omitempty"` // the tracker's name for it: "In Progress"
	Stage         string    `json:"stage,omitempty"` // TicketTodo when ""
	Project       string    `json:"project,omitempty"`
	Priority      int       `json:"priority,omitempty"`       // 1 the most urgent, then 2, 3…; 0 none
	PriorityLabel string    `json:"priority_label,omitempty"` // "High"
	Blocked       bool      `json:"blocked,omitempty"`        // another open ticket blocks it
	BlockedBy     []string  `json:"blocked_by,omitempty"`     // which, when the tracker says
	PR            *TicketPR `json:"pr,omitempty"`             // the newest pull request on it
	Summary       string    `json:"summary,omitempty"`        // the description's start
	Facts         []string  `json:"facts,omitempty"`          // "2 attachments", "linked PR #4398", "4 comments"
	// Context is markdown: the ticket, its comments and attachments, handed to the agent with the prompt.
	Context string `json:"context,omitempty"`
	Tracker string `json:"tracker,omitempty"` // the tracker's Title: "Linear"
}

// Started says the work on it has begun: in progress or in review.
func (t Ticket) Started() bool { return t.Stage == TicketStarted || t.Stage == TicketInReview }

// TicketPR is a pull request on a ticket.
type TicketPR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Repo   string `json:"repo,omitempty"`  // owner/name
	Title  string `json:"title,omitempty"` // given when it's linked
}

// TicketMove is what a move did: Moved false when the ticket was there or past it already. State is where it is.
type TicketMove struct {
	Moved bool   `json:"moved"`
	State string `json:"state,omitempty"`
}

// TicketTracker is a ticket tracker (Linear, Jira…), as its plugin brings it. The app builds everything else on it:
// the omnibox's tickets, the launcher's card, the moves as a session starts from a ticket and its PR is seen and
// merged, and the assistant's my_tickets. The funcs may block and are called off the app's loop; any but Lookup may be nil.
type TicketTracker struct {
	Name  string // "linear"
	Title string // "Linear"
	Order int
	Keys  *regexp.Regexp // the keys it takes (ENG-2231); nil takes every key
	// Secret is the secret it needs set before it can answer (LINEAR_TOKEN): until it is, the app asks for it.
	Secret string

	Assigned func(ctx context.Context) ([]Ticket, error) // the open tickets assigned to you
	Lookup   func(ctx context.Context, key string) (Ticket, error)
	Start    func(ctx context.Context, key string) (TicketMove, error) // into progress: a session started from it
	Review   func(ctx context.Context, key string) (TicketMove, error) // into review: its PR is open
	Done     func(ctx context.Context, key string) (TicketMove, error) // into done: its session's PRs merged
	Link     func(ctx context.Context, key string, pr TicketPR) error  // the PR attached to it
	Source   string
}

func (TicketTracker) ContributionKind() string   { return "ticket.tracker" }
func (t TicketTracker) ContributionName() string { return t.Name }

// Takes says whether key is one of the tracker's.
func (t TicketTracker) Takes(key string) bool {
	key = strings.TrimSpace(key)
	return key != "" && (t.Keys == nil || t.Keys.MatchString(key))
}

// Label is how the tracker is named to the user.
func (t TicketTracker) Label() string { return cmp.Or(t.Title, t.Name) }

// TicketTrackers are the trackers by Order, then name.
func TicketTrackers(r Registry) []TicketTracker {
	var out []TicketTracker
	for _, c := range r.Contributions("ticket.tracker") {
		out = append(out, c.(TicketTracker))
	}
	slices.SortFunc(out, func(a, b TicketTracker) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}

// TrackerFor is the first tracker that takes key; ok is false when none does.
func TrackerFor(r Registry, key string) (TicketTracker, bool) {
	for _, t := range TicketTrackers(r) {
		if t.Lookup != nil && t.Takes(key) {
			return t, true
		}
	}
	return TicketTracker{}, false
}

// LookupTicket asks t for key's ticket, its Tracker filled in.
func LookupTicket(ctx context.Context, t TicketTracker, key string) (Ticket, error) {
	var tk Ticket
	err := Call(func() (err error) { tk, err = t.Lookup(ctx, strings.TrimSpace(key)); return err })
	if err != nil {
		return Ticket{}, err
	}
	tk.Tracker = t.Label()
	return tk, nil
}

// AssignedTickets asks t for the tickets assigned to you, each's Tracker filled in; none when it can't list them.
func AssignedTickets(ctx context.Context, t TicketTracker) ([]Ticket, error) {
	if t.Assigned == nil {
		return nil, nil
	}
	var out []Ticket
	err := Call(func() (err error) { out, err = t.Assigned(ctx); return err })
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Tracker = t.Label()
	}
	return out, nil
}

// SortTickets puts tickets in the order the work happens in (crit's): priority (most urgent first, none last),
// blocked last in its band, then started, then with a pull request, then by key so it never wobbles.
func SortTickets(ts []Ticket) { slices.SortStableFunc(ts, CompareTickets) }

// CompareTickets is SortTickets' order.
func CompareTickets(a, b Ticket) int {
	if c := cmp.Compare(priorityRank(a.Priority), priorityRank(b.Priority)); c != 0 {
		return c
	}
	if a.Blocked != b.Blocked {
		return boolFirst(b.Blocked)
	}
	if a.Started() != b.Started() {
		return boolFirst(a.Started())
	}
	if ap, bp := a.PR != nil, b.PR != nil; ap != bp {
		return boolFirst(ap)
	}
	return CompareTicketKeys(a.Key, b.Key)
}

func boolFirst(a bool) int {
	if a {
		return -1
	}
	return 1
}

func priorityRank(p int) int {
	if p <= 0 {
		return 1 << 30
	}
	return p
}

// CompareTicketKeys orders keys by prefix, then numerically: FIX-9 before FIX-10.
func CompareTicketKeys(a, b string) int {
	pa, na := splitTicketKey(a)
	pb, nb := splitTicketKey(b)
	return cmp.Or(strings.Compare(pa, pb), cmp.Compare(na, nb), strings.Compare(a, b))
}

func splitTicketKey(k string) (string, int) {
	i := strings.LastIndex(k, "-")
	if i < 0 {
		return k, 0
	}
	n, err := strconv.Atoi(k[i+1:])
	if err != nil {
		return k, 0
	}
	return k[:i], n
}
