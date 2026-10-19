package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// ticketsStale is how long a tracker's list is kept before it's asked again (crit's refresh).
const ticketsStale = 10 * time.Minute

// ticketsPlugin is everything about tickets that isn't a tracker's own, built once on every registry.TicketTracker:
// the tickets page, the omnibox's tickets (`!`), Continue on a ticket that has a session, the ask for a tracker's
// key, the moves and the PR link as a session started from a ticket goes on, and the assistant's my_tickets.
type ticketsPlugin struct {
	disposer
	h plugin.Host

	mu      sync.Mutex
	lists   map[string]*ticketList   // by tracker
	details map[string]*ticketDetail // by key, as the page asked
	page    ticketsPage
}

// ticketList is what a tracker last said of the tickets assigned to you.
type ticketList struct {
	tickets  []registry.Ticket
	err      string
	answered bool
	at       time.Time     // when it answered; zero to ask again
	busy     chan struct{} // closed when the ask under way answers
	since    time.Time     // when the ask under way started
}

// ticketRow is a row of the omnibox's tickets: a ticket, or what a tracker needs first.
type ticketRow struct {
	tracker registry.TicketTracker
	ticket  *registry.Ticket
	action  string // "key" or "refresh" when there's no ticket
	err     string
}

func (x *ticketsPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "tickets"} }

func (x *ticketsPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h, x.lists, x.details = h, map[string]*ticketList{}, map[string]*ticketDetail{}
	x.add(h.Picker(x.source()))
	has := func(registry.Args) bool { return x.any() }
	open := func(registry.Args) bool { return x.page.open }
	for _, c := range []registry.Command{
		{ID: "Tickets", Title: "The tickets page: your tickets from every tracker; :Tickets linear for one's", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.openPage(a.String("args"))
		}},
		{ID: "tickets.open", Title: "Open the tickets page: every tracker's tickets, or one {tracker}'s", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.openPage(a.String("tracker"))
		}},
		{ID: "tickets.refresh", Title: "Ask every tracker for your tickets again now", Run: func(registry.Ctx, registry.Args) error {
			x.refreshAll()
			return nil
		}},
		{ID: "tickets.plan", Title: "Plan the ticket selected on the tickets page, or continue its session", Available: open,
			Run: func(registry.Ctx, registry.Args) error { return x.pageCommand("plan") }},
		{ID: "tickets.work", Title: "Start work on the ticket selected on the tickets page, with no plan", Available: open,
			Run: func(registry.Ctx, registry.Args) error { return x.pageCommand("work") }},
		{ID: "tickets.browse", Title: "Open the ticket selected on the tickets page in its tracker, in the browser", Available: open,
			Run: func(registry.Ctx, registry.Args) error { return x.pageCommand("browse") }},
		{ID: "tickets.filter", Title: "Filter the tickets page's list", Available: open,
			Run: func(registry.Ctx, registry.Args) error { x.page.focusFilter = true; return nil }},
		{ID: "tickets.key", Title: "Set a ticket tracker's API key: {tracker}, else the first still without one", Run: func(ctx registry.Ctx, a registry.Args) error {
			return x.askKey(ctx, a.String("tracker"))
		}},
		{ID: "TicketKey", Title: "Set a ticket tracker's API key; :TicketKey linear", Run: func(ctx registry.Ctx, a registry.Args) error {
			return x.askKey(ctx, strings.TrimSpace(a.String("args")))
		}},
	} {
		c.Source = "builtin:tickets"
		if c.Available == nil {
			c.Available = has
		}
		x.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Keys: "<CR>", Command: "tickets.plan", Desc: "Plan, or continue"},
		{Keys: "w", Command: "tickets.work", Desc: "Work, with no plan"},
		{Keys: "o", Command: "tickets.browse", Desc: "Open in the browser"},
		{Keys: "r", Command: "tickets.refresh", Desc: "Refresh"},
		{Keys: "/", Command: "tickets.filter", Desc: "Filter"},
	} {
		k.Mode = TicketsKeyMode
		x.add(h.Keymap(k))
	}
	x.add(h.View(OverlayPrefix+"tickets", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "tickets", Fn: x.pageView} })))
	x.add(registry.On(h, registry.Hook{Kind: registry.Observe, Label: "the ticket's tracker moves it into progress"}, func(_ registry.Ctx, ev *SessionStart) (registry.Verdict, error) {
		x.started(*ev)
		return registry.Verdict{}, nil
	}))
	x.add(registry.On(h, registry.Hook{Kind: registry.Observe, Label: "the ticket's tracker links the PR, and moves it into review"}, func(_ registry.Ctx, ev *PrSeen) (registry.Verdict, error) {
		x.prSeen(*ev)
		return registry.Verdict{}, nil
	}))
	x.add(registry.On(h, registry.Hook{Kind: registry.Observe, Label: "the ticket's tracker moves it to done once the session's PRs are merged"}, func(_ registry.Ctx, ev *PrMerged) (registry.Verdict, error) {
		x.prMerged(*ev)
		return registry.Verdict{}, nil
	}))
	x.add(h.Contribute(registry.AgentTool{Name: "my_tickets", For: []string{assistantAgent}, ReadOnly: true, Reads: "tickets",
		Source: "builtin:tickets", Run: x.toolMyTickets,
		Desc: "The tickets assigned to the user in every ticket tracker (Linear…), most pressing first: key, tracker, title, state, project, priority, whether it's blocked, its PR and URL."}))
	return nil
}

// any says whether a tracker is registered.
func (x *ticketsPlugin) any() bool { return len(registry.TicketTrackers(x.h)) > 0 }

// needsKey says the tracker can't answer until its secret is set.
func (x *ticketsPlugin) needsKey(tr registry.TicketTracker) bool {
	if tr.Secret == "" {
		return false
	}
	_, ok := x.h.Secrets().Secret(tr.Secret)
	return !ok
}

func (x *ticketsPlugin) askKey(ctx registry.Ctx, name string) error {
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

// ticketSession is the session to continue a ticket in, rather than start another: one whose ticket is key.
func ticketSession(h plugin.Host, key string) (core.Session, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, false
	}
	return newestSession(h, func(st core.SessionState) bool { return strings.EqualFold(strings.TrimSpace(st.Ticket), key) })
}

// newestSession is, of the sessions match takes, a live one before an archived one, then the newest.
func newestSession(h plugin.Host, match func(core.SessionState) bool) (core.Session, bool) {
	var best core.Session
	var bestSt core.SessionState
	for _, se := range h.Sessions() {
		st := se.Snapshot()
		if !match(st) {
			continue
		}
		if best == nil || bestSt.Archived && !st.Archived || bestSt.Archived == st.Archived && !st.Created.Before(bestSt.Created) {
			best, bestSt = se, st
		}
	}
	return best, best != nil
}

// continueLabel is how a ticket with a session says so: "Continue · fix-prices".
func continueLabel(se core.Session) string { return "Continue · " + se.Name() }

// fresh asks a tracker for its list when it has none or it's stale, and gives the ask under way, if any.
func (x *ticketsPlugin) fresh(tr registry.TicketTracker) <-chan struct{} {
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
	l.busy, l.since = done, windowNow(x.h)
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
func (x *ticketsPlugin) stale(name string) {
	x.mu.Lock()
	if l := x.lists[name]; l != nil {
		l.at = time.Time{}
	}
	x.mu.Unlock()
}

func (x *ticketsPlugin) refreshAll() {
	for _, tr := range registry.TicketTrackers(x.h) {
		x.stale(tr.Name)
		x.fresh(tr)
	}
}

// listAt is when a tracker last answered; zero if it hasn't.
func (x *ticketsPlugin) listAt(name string) time.Time {
	x.mu.Lock()
	defer x.mu.Unlock()
	if l := x.lists[name]; l != nil && l.answered {
		return l.at
	}
	return time.Time{}
}

// pendingFirst reports a list's first fetch running; one never asked for isn't loading.
func pendingFirst(s widget.LoadState) bool { return s.Phase == widget.Pending && !s.Stale }

// listState is a tracker's list as the loading widgets read it.
func (x *ticketsPlugin) listState(name string) widget.LoadState {
	x.mu.Lock()
	defer x.mu.Unlock()
	l := x.lists[name]
	switch {
	case l == nil:
		return widget.LoadState{}
	case l.busy != nil:
		return widget.LoadState{Phase: widget.Pending, Since: l.since, Stale: l.answered}
	case l.err != "" && l.tickets == nil:
		return widget.LoadState{Phase: widget.Failed, Err: errors.New(l.err)}
	}
	return widget.LoadState{Phase: widget.Ready}
}

// list is a tracker's tickets as last told, and why there are none.
func (x *ticketsPlugin) list(name string) ([]registry.Ticket, string, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	l := x.lists[name]
	if l == nil {
		return nil, "", false
	}
	return l.tickets, l.err, l.answered
}

// rows are the omnibox's tickets: each tracker's key ask or failure, then every tracker's tickets in one order.
func (x *ticketsPlugin) rows() []ticketRow {
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

func (x *ticketsPlugin) source() registry.PickerSource {
	return registry.PickerSource{ID: "tickets", Title: "Tickets", Prefix: "!", Order: 40, Available: x.any,
		Loading: func() bool {
			for _, tr := range registry.TicketTrackers(x.h) {
				if tr.Assigned != nil && !x.needsKey(tr) && pendingFirst(x.listState(tr.Name)) {
					return true
				}
			}
			return false
		},
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
					if se, ok := ticketSession(x.h, t.Key); ok {
						it.Note = continueLabel(se) // the PR and priority are in the preview
					}
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
					hint := "↵ starts work on it: a plan, the ticket its context"
					if se, ok := ticketSession(x.h, t.Key); ok {
						previewField(c, "session", se.Name())
						hint = "↵ continues it in " + se.Name() + ", resumed if it's stopped or archived"
					}
					c.Text("hint", hint, uiFont(11, th.C(theme.FgFaint)))
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
			if se, ok := ticketSession(x.h, r.ticket.Key); ok {
				return x.h.Run(ctx, "session.open", registry.Args{"id": string(se.ID())})
			}
			return x.h.Run(ctx, "session.new", registry.Args{"kind": string(core.KindPlan), "ticket": r.ticket.Key})
		},
	}
}

// moved tells the user a ticket moved, and asks its tracker for the list again.
func (x *ticketsPlugin) moved(tr registry.TicketTracker, key string, m registry.TicketMove) {
	if !m.Moved {
		return
	}
	x.stale(tr.Name)
	x.h.Notify(core.Notice{ID: "tickets.moved", Severity: core.SevInfo, Source: tr.Source,
		Text: key + " moved to " + m.State + " in " + tr.Label(), Time: time.Now()})
}

// trackerCall runs a call to a tracker off the loop, with a time limit; a failure goes to the log.
func (x *ticketsPlugin) trackerCall(tr registry.TicketTracker, what string, fn func(ctx context.Context) error) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := registry.Call(func() error { return fn(ctx) }); err != nil {
			x.h.Log().Warn("tickets: "+tr.Label()+" couldn't "+what, "err", err)
		}
	}()
}

// started moves a session's ticket into progress as the session starts from it.
func (x *ticketsPlugin) started(ev SessionStart) {
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
func (x *ticketsPlugin) prSeen(ev PrSeen) {
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

// prMerged moves a session's ticket to done once the last of its open PRs merges, whoever merged it.
func (x *ticketsPlugin) prMerged(ev PrMerged) {
	tr, ok := registry.TrackerFor(x.h, ev.Ticket)
	if !ok || tr.Done == nil || ev.Open > 0 {
		return
	}
	key := strings.TrimSpace(ev.Ticket)
	x.trackerCall(tr, "move "+key+" to done", func(ctx context.Context) error {
		m, err := tr.Done(ctx, key)
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
func (x *ticketsPlugin) toolMyTickets(ctx registry.Ctx, _ map[string]any) (any, error) {
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
