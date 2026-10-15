package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// ticketsStale is how long a tracker's list is kept before it's asked again (crit's refresh).
const ticketsStale = 10 * time.Minute

// ticketsExt is everything about tickets that isn't a tracker's own, built once on every registry.TicketTracker:
// the omnibox's tickets (`!`), the ask for a tracker's key, the moves and the PR link as a session started from a
// ticket goes on, and the assistant's my_tickets.
type ticketsExt struct {
	disposer
	h ext.Host

	mu    sync.Mutex
	lists map[string]*ticketList // by tracker
}

// ticketList is what a tracker last said of the tickets assigned to you.
type ticketList struct {
	tickets  []registry.Ticket
	err      string
	answered bool
	at       time.Time     // when it answered; zero to ask again
	busy     chan struct{} // closed when the ask under way answers
}

// ticketRow is a row of the omnibox's tickets: a ticket, or what a tracker needs first.
type ticketRow struct {
	tracker registry.TicketTracker
	ticket  *registry.Ticket
	action  string // "key" or "refresh" when there's no ticket
	err     string
}

func (x *ticketsExt) Manifest() ext.Manifest { return ext.Manifest{ID: "tickets"} }

func (x *ticketsExt) Activate(_ context.Context, h ext.Host) error {
	x.h, x.lists = h, map[string]*ticketList{}
	x.add(h.Picker(x.source()))
	has := func(registry.Args) bool { return x.any() }
	for _, c := range []registry.Command{
		{ID: "Tickets", Title: "Your tickets from every tracker, in the omnibox", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "omnibox.open", registry.Args{"scope": "tickets"})
		}},
		{ID: "tickets.refresh", Title: "Ask every tracker for your tickets again now", Run: func(registry.Ctx, registry.Args) error {
			x.refreshAll()
			return nil
		}},
		{ID: "tickets.key", Title: "Set a ticket tracker's API key: {tracker}, else the first still without one", Run: func(ctx registry.Ctx, a registry.Args) error {
			return x.askKey(ctx, a.String("tracker"))
		}},
		{ID: "TicketKey", Title: "Set a ticket tracker's API key; :TicketKey linear", Run: func(ctx registry.Ctx, a registry.Args) error {
			return x.askKey(ctx, strings.TrimSpace(a.String("args")))
		}},
	} {
		c.Source, c.Available = "builtin:tickets", has
		x.add(h.Command(c))
	}
	x.add(registry.On(h, registry.Hook{Kind: registry.Observe, Label: "the ticket's tracker moves it into progress"}, func(_ registry.Ctx, ev *SessionStart) (registry.Verdict, error) {
		x.started(*ev)
		return registry.Verdict{}, nil
	}))
	x.add(registry.On(h, registry.Hook{Kind: registry.Observe, Label: "the ticket's tracker links the PR, and moves it into review"}, func(_ registry.Ctx, ev *PrSeen) (registry.Verdict, error) {
		x.prSeen(*ev)
		return registry.Verdict{}, nil
	}))
	x.add(h.Contribute(registry.AgentTool{Name: "my_tickets", For: []string{assistantAgent}, ReadOnly: true, Reads: "tickets",
		Source: "builtin:tickets", Run: x.toolMyTickets,
		Desc: "The tickets assigned to the user in every ticket tracker (Linear…), most pressing first: key, tracker, title, state, project, priority, whether it's blocked, its PR and URL."}))
	return nil
}

// any says whether a tracker is registered.
func (x *ticketsExt) any() bool { return len(registry.TicketTrackers(x.h)) > 0 }

// needsKey says the tracker can't answer until its secret is set.
func (x *ticketsExt) needsKey(tr registry.TicketTracker) bool {
	if tr.Secret == "" {
		return false
	}
	_, ok := x.h.Secrets().Secret(tr.Secret)
	return !ok
}

func (x *ticketsExt) askKey(ctx registry.Ctx, name string) error {
	for _, tr := range registry.TicketTrackers(x.h) {
		if tr.Secret != "" && (tr.Name == name || name == "" && x.needsKey(tr)) {
			return x.h.Run(ctx, "secrets.set", registry.Args{"name": tr.Secret})
		}
	}
	if name == "" {
		return fmt.Errorf("no ticket tracker is waiting for a key")
	}
	return fmt.Errorf("no ticket tracker %q takes a key", name)
}

// fresh asks a tracker for its list when it has none or it's stale, and gives the ask under way, if any.
func (x *ticketsExt) fresh(tr registry.TicketTracker) <-chan struct{} {
	if tr.Assigned == nil || x.needsKey(tr) {
		return nil
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	l := x.lists[tr.Name]
	if l == nil {
		l = &ticketList{}
		x.lists[tr.Name] = l
	}
	if l.busy != nil {
		return l.busy
	}
	if !l.at.IsZero() && time.Since(l.at) < ticketsStale {
		return nil
	}
	done := make(chan struct{})
	l.busy = done
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		ts, err := registry.AssignedTickets(ctx, tr)
		x.mu.Lock()
		l.busy, l.at, l.answered = nil, time.Now(), true
		if err != nil {
			l.err = err.Error()
		} else {
			l.tickets, l.err = ts, ""
		}
		x.mu.Unlock()
		close(done)
		x.h.Redraw()
	}()
	return done
}

// stale marks a tracker's list to be asked for again.
func (x *ticketsExt) stale(name string) {
	x.mu.Lock()
	if l := x.lists[name]; l != nil {
		l.at = time.Time{}
	}
	x.mu.Unlock()
}

func (x *ticketsExt) refreshAll() {
	for _, tr := range registry.TicketTrackers(x.h) {
		x.stale(tr.Name)
		x.fresh(tr)
	}
}

// list is a tracker's tickets as last told, and why there are none.
func (x *ticketsExt) list(name string) ([]registry.Ticket, string, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	l := x.lists[name]
	if l == nil {
		return nil, "", false
	}
	return l.tickets, l.err, l.answered
}

// rows are the omnibox's tickets: each tracker's key ask or failure, then every tracker's tickets in one order.
func (x *ticketsExt) rows() []ticketRow {
	var out []ticketRow
	var all []registry.Ticket
	trackers := map[string]registry.TicketTracker{}
	for _, tr := range registry.TicketTrackers(x.h) {
		if tr.Assigned == nil {
			continue
		}
		trackers[tr.Label()] = tr
		if x.needsKey(tr) {
			out = append(out, ticketRow{tracker: tr, action: "key"})
			continue
		}
		x.fresh(tr)
		ts, err, _ := x.list(tr.Name)
		if err != "" && ts == nil {
			out = append(out, ticketRow{tracker: tr, action: "refresh", err: err})
		}
		all = append(all, ts...)
	}
	registry.SortTickets(all)
	for i := range all {
		out = append(out, ticketRow{tracker: trackers[all[i].Tracker], ticket: &all[i]})
	}
	return out
}

// stageTone is a stage's colour: violet before it starts, cyan in progress, pink in review.
func stageTone(stage string) string {
	switch stage {
	case registry.TicketStarted:
		return "status.working"
	case registry.TicketInReview:
		return "status.reviewer"
	}
	return "status.permission"
}

// ticketNote is a row's note: its PR, blocked, its priority.
func ticketNote(t registry.Ticket) string {
	var parts []string
	if t.PR != nil {
		parts = append(parts, fmt.Sprintf("PR #%d", t.PR.Number))
	}
	if t.Blocked {
		parts = append(parts, "blocked")
	}
	if t.PriorityLabel != "" {
		parts = append(parts, strings.ToLower(t.PriorityLabel))
	}
	return strings.Join(parts, " · ")
}

func (x *ticketsExt) source() registry.PickerSource {
	return registry.PickerSource{ID: "tickets", Title: "Tickets", Prefix: "!", Order: 40, Available: x.any,
		Items: func(registry.Ctx, string) []registry.PickerItem {
			var out []registry.PickerItem
			for _, r := range x.rows() {
				it := registry.PickerItem{Icon: "◆", IconStyle: "status.permission", Data: r}
				switch r.action {
				case "key":
					it.ID, it.Label, it.Detail = "key:"+r.tracker.Name, "Set your "+r.tracker.Label()+" API key", "to list the tickets assigned to you"
				case "refresh":
					it.ID, it.Label, it.IconStyle = "err:"+r.tracker.Name, r.tracker.Label()+": "+r.err, "warn"
				default:
					t := r.ticket
					it.ID, it.Label, it.Detail, it.Note = t.Key, t.Key+" "+t.Title, t.Project, ticketNote(*t)
					it.Badge, it.BadgeStyle = t.State, stageTone(t.Stage)
				}
				out = append(out, it)
			}
			return out
		},
		Preview: func(it registry.PickerItem) ui.Node {
			r, _ := it.Data.(ticketRow)
			return &ui.Func{Key: "ticket", Fn: func(c *ui.Ctx) {
				th := c.Theme()
				c.Column("p", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
					if r.action != "" {
						c.Text("t", it.Label, uiFont(12, th.C(theme.FgMuted)))
						return
					}
					t := r.ticket
					tone := ui.TextStyle{Color: tokenColour(th, stageTone(t.Stage), th.C(theme.Accent))}
					previewHead(c, t.Key+" · "+strings.ToUpper(t.State), tone, t.Title)
					previewField(c, "tracker", t.Tracker)
					if t.Project != "" {
						previewField(c, "project", t.Project)
					}
					if t.PriorityLabel != "" {
						previewField(c, "priority", t.PriorityLabel)
					}
					if t.PR != nil {
						previewField(c, "pr", strings.TrimSpace(fmt.Sprintf("#%d %s", t.PR.Number, t.PR.Repo)))
					}
					if t.Blocked {
						previewField(c, "blocked by", cmpOr(strings.Join(t.BlockedBy, ", "), "another ticket"))
					}
					c.Text("hint", "↵ starts work on it: a plan, the ticket its context", uiFont(11, th.C(theme.FgFaint)))
				})
			}}
		},
		Run: func(ctx registry.Ctx, it registry.PickerItem) error {
			r, _ := it.Data.(ticketRow)
			switch r.action {
			case "key":
				return x.h.Run(ctx, "secrets.set", registry.Args{"name": r.tracker.Secret})
			case "refresh":
				x.stale(r.tracker.Name)
				x.fresh(r.tracker)
				return nil
			}
			return x.h.Run(ctx, "session.new", registry.Args{"kind": string(core.KindPlan), "ticket": r.ticket.Key})
		},
	}
}

// moved tells the user a ticket moved, and asks its tracker for the list again.
func (x *ticketsExt) moved(tr registry.TicketTracker, key string, m registry.TicketMove) {
	if !m.Moved {
		return
	}
	x.stale(tr.Name)
	x.h.Notify(core.Notice{ID: "tickets.moved", Severity: core.SevInfo, Source: tr.Source,
		Text: key + " moved to " + m.State + " in " + tr.Label(), Time: time.Now()})
}

// trackerCall runs a call to a tracker off the loop, with a time limit; a failure goes to the log.
func (x *ticketsExt) trackerCall(tr registry.TicketTracker, what string, fn func(ctx context.Context) error) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := registry.Call(func() error { return fn(ctx) }); err != nil {
			x.h.Log().Warn("tickets: "+tr.Label()+" couldn't "+what, "err", err)
		}
	}()
}

// started moves a session's ticket into progress as the session starts from it.
func (x *ticketsExt) started(ev SessionStart) {
	tr, ok := registry.TrackerFor(x.h, ev.Ticket)
	if !ok || tr.Start == nil {
		return
	}
	key := strings.TrimSpace(ev.Ticket)
	x.trackerCall(tr, "start "+key, func(ctx context.Context) error {
		m, err := tr.Start(ctx, key)
		if err == nil {
			x.moved(tr, key, m)
		}
		return err
	})
}

// prSeen links a session's PR to its ticket, whoever raised it, and moves the ticket into review while it's open.
func (x *ticketsExt) prSeen(ev PrSeen) {
	tr, ok := registry.TrackerFor(x.h, ev.Ticket)
	if !ok {
		return
	}
	key := strings.TrimSpace(ev.Ticket)
	x.trackerCall(tr, "link "+ev.URL+" to "+key, func(ctx context.Context) error {
		if tr.Link != nil {
			if err := tr.Link(ctx, key, registry.TicketPR{Number: ev.Number, URL: ev.URL, Title: ev.Title}); err != nil {
				return err
			}
			x.stale(tr.Name)
		}
		if tr.Review == nil || ev.State != "open" || ev.Merged {
			return nil
		}
		m, err := tr.Review(ctx, key)
		if err == nil {
			x.moved(tr, key, m)
		}
		return err
	})
}

// myTicket is a ticket as the assistant reads it.
type myTicket struct {
	Key      string `json:"key"`
	Tracker  string `json:"tracker"`
	Title    string `json:"title"`
	State    string `json:"state,omitempty"`
	Project  string `json:"project,omitempty"`
	Priority string `json:"priority,omitempty"`
	Blocked  bool   `json:"blocked,omitempty"`
	PR       string `json:"pr,omitempty"`
	URL      string `json:"url,omitempty"`
}

// toolMyTickets is the assistant's my_tickets: every tracker's tickets as the omnibox lists them, asked for first
// when they're stale, and why a tracker has none.
func (x *ticketsExt) toolMyTickets(ctx registry.Ctx, _ map[string]any) (any, error) {
	trackers := registry.TicketTrackers(x.h)
	var waits []<-chan struct{}
	for _, tr := range trackers {
		if w := x.fresh(tr); w != nil {
			waits = append(waits, w)
		}
	}
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for _, w := range waits {
		select {
		case <-w:
		case <-wait.Done():
		}
	}
	var all []registry.Ticket
	var notes []string
	for _, tr := range trackers {
		if tr.Assigned == nil {
			continue
		}
		ts, err, ok := x.list(tr.Name)
		switch {
		case x.needsKey(tr):
			notes = append(notes, "No "+tr.Label()+" API key is set yet: :TicketKey "+tr.Name+" sets one.")
		case !ok:
			notes = append(notes, tr.Label()+" hasn't answered yet: ask again in a moment.")
		case err != "":
			notes = append(notes, tr.Label()+": "+err)
		}
		all = append(all, ts...)
	}
	if len(notes) == 0 && len(all) == 0 && len(trackers) == 0 {
		return "No ticket tracker is installed: a tracker's plugin (Linear…) brings the user's tickets.", nil
	}
	registry.SortTickets(all)
	out := make([]myTicket, 0, len(all))
	for _, t := range all {
		m := myTicket{Key: t.Key, Tracker: t.Tracker, Title: t.Title, State: t.State, Project: t.Project, Priority: t.PriorityLabel,
			Blocked: t.Blocked, URL: t.URL}
		if t.PR != nil {
			m.PR = t.PR.URL
		}
		out = append(out, m)
	}
	if len(notes) == 0 {
		return out, nil
	}
	if len(out) == 0 {
		return strings.Join(notes, "\n"), nil
	}
	return map[string]any{"tickets": out, "notes": notes}, nil
}
