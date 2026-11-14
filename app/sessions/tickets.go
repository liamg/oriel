package sessions

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit/work"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// ticketsStale is how long a tracker's list is kept before it's asked again (crit's refresh).
const ticketsStale = 10 * time.Minute

// Tickets is everything about tickets that isn't a tracker's own, built once on every registry.TicketTracker:
// the tickets page, the omnibox's tickets (`!`), Continue on a ticket that has a session, the ask for a tracker's
// key, the moves and the PR link as a session started from a ticket goes on, and the assistant's my_tickets.
type Tickets struct {
	kit.Disposer
	h    plugin.Host
	work work.Work // the trackers' calls, stopped with it

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
	at       time.Time    // when it answered; zero to ask again
	ask      work.Refetch // the ask under way
	since    time.Time    // when the ask under way started
}

// ticketRow is a row of the omnibox's tickets: a ticket, or what a tracker needs first.
type ticketRow struct {
	tracker registry.TicketTracker
	ticket  *registry.Ticket
	action  string // "key" or "refresh" when there's no ticket
	err     string
}

func (x *Tickets) Manifest() plugin.Manifest { return plugin.Manifest{ID: "tickets"} }

func (x *Tickets) Activate(_ context.Context, h plugin.Host) error {
	x.Add(h.Contribute(registry.Busy{Name: "tickets", Busy: x.work.Busy, Source: "builtin:tickets"}))
	x.h, x.lists, x.details = h, map[string]*ticketList{}, map[string]*ticketDetail{}
	x.Add(h.Contribute(kit.PagePlace("tickets", "Tickets", "tickets.open", &x.page.open)))
	x.Add(h.Picker(x.source()))
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Keys: "<CR>", Command: "tickets.plan", Desc: "Plan, or continue"},
		{Keys: "w", Command: "tickets.work", Desc: "Work, with no plan"},
		{Keys: "o", Command: "tickets.browse", Desc: "Open in the browser"},
		{Keys: "r", Command: "tickets.refresh", Desc: "Refresh"},
		{Keys: "/", Command: "tickets.filter", Desc: "Filter"},
	} {
		k.Mode = TicketsKeyMode
		x.Add(h.Keymap(k))
	}
	x.Add(h.View(kit.OverlayPrefix+"tickets", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return &ui.Func{Key: "tickets", Fn: x.pageView} })))
	x.Add(h.View(kit.TicketView, ticketTab{x}))
	x.Add(registry.On(h, registry.Hook{Kind: registry.Observe, Label: "the ticket's tracker moves it into progress"}, func(_ registry.Ctx, ev *kit.SessionStart) (registry.Verdict, error) {
		x.started(*ev)
		return registry.Verdict{}, nil
	}))
	x.Add(registry.On(h, registry.Hook{Kind: registry.Observe, Label: "the ticket's tracker links the PR, and moves it into review"}, func(_ registry.Ctx, ev *kit.PrSeen) (registry.Verdict, error) {
		x.prSeen(*ev)
		return registry.Verdict{}, nil
	}))
	x.Add(registry.On(h, registry.Hook{Kind: registry.Observe, Label: "the ticket's tracker moves it to done once the session's PRs are merged"}, func(_ registry.Ctx, ev *kit.PrMerged) (registry.Verdict, error) {
		x.prMerged(*ev)
		return registry.Verdict{}, nil
	}))
	x.Add(h.Contribute(registry.AgentTool{Name: "my_tickets", For: []string{kit.AssistantAgent}, ReadOnly: true, Reads: "tickets",
		Source: "builtin:tickets", Run: x.toolMyTickets,
		Desc: "The tickets assigned to the user in every ticket tracker (Linear…), most pressing first: key, tracker, title, state, project, priority, whether it's blocked, its PR and URL."}))
	return nil
}

func (x *Tickets) commands() []registry.Command {
	has := func(registry.Args) bool { return x.any() }
	open := func(registry.Args) bool { return x.page.open }
	out := []registry.Command{
		{ID: "Tickets", Title: "The tickets page: your tickets from every tracker; :Tickets linear for one's", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.openPage(a.String("args"))
		}},
		{ID: "tickets.open", Title: "Open the tickets page: every tracker's tickets, or one {tracker}'s", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.openPage(a.String("tracker"))
		}},
		{ID: "ticket.open", Title: "Open {ticket} in a tab of its own: its title, state, fields and description in full", Run: func(ctx registry.Ctx, a registry.Args) error {
			key := strings.TrimSpace(a.String("ticket"))
			if key == "" {
				return fmt.Errorf("say which {ticket}")
			}
			return x.h.Run(ctx, "view.open", registry.Args{"id": kit.TicketView, "instance": key, "place": "tab"})
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
		{ID: "tickets.done", Title: "Mark a {ticket} done in its tracker", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.markDone(strings.TrimSpace(a.String("ticket")))
		}},
		{ID: "tickets.ask-done", Title: "Ask whether a {ticket} is done, as merging its session's last PR does; {session} is the session it's for",
			Run: func(_ registry.Ctx, a registry.Args) error {
				x.askDone(strings.TrimSpace(a.String("ticket")), core.SessionID(a.String("session")))
				return nil
			}},
		{ID: "TicketKey", Title: "Set a ticket tracker's API key; :TicketKey linear", Run: func(ctx registry.Ctx, a registry.Args) error {
			return x.askKey(ctx, strings.TrimSpace(a.String("args")))
		}},
	}
	for i := range out {
		c := &out[i]
		c.Source = "builtin:tickets"
		if c.Available == nil {
			c.Available = has
		}
	}
	return out
}

// any says whether a tracker is registered.
func (x *Tickets) any() bool { return len(registry.TicketTrackers(x.h)) > 0 }

// needsKey says the tracker can't answer until its secret is set.
func (x *Tickets) needsKey(tr registry.TicketTracker) bool {
	if tr.Secret == "" {
		return false
	}
	_, ok := x.h.Secrets().Secret(tr.Secret)
	return !ok
}

func (x *Tickets) askKey(ctx registry.Ctx, name string) error {
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
	return kit.NewestSession(h, func(st core.SessionState) bool { return strings.EqualFold(strings.TrimSpace(st.Ticket), key) })
}

// continueLabel is how a ticket with a session says so: "Continue · fix-prices".
func continueLabel(se core.Session) string { return "Continue · " + se.Name() }

// fresh asks a tracker for its list when it has none or it's stale, and gives the ask under way, if any.
func (x *Tickets) fresh(tr registry.TicketTracker, refresh bool) <-chan struct{} {
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
	if busy := l.ask.Busy(); busy != nil {
		l.ask.Begin(refresh) // a refresh: the ask under way may have started before what it's for
		return busy
	}
	if !l.at.IsZero() && time.Since(l.at) < ticketsStale {
		return nil
	}
	done, _ := l.ask.Begin(false)
	l.since = kit.WindowNow(x.h)
	x.work.Go(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		var ts []registry.Ticket
		var err error
		for {
			ts, err = registry.AssignedTickets(ctx, tr)
			x.mu.Lock()
			again := l.ask.Again() && err == nil && ctx.Err() == nil
			x.mu.Unlock()
			if !again {
				break
			}
		}
		x.mu.Lock()
		l.ask.End()
		l.at, l.answered = time.Now(), true
		if err != nil {
			l.err = err.Error()
		} else {
			l.tickets, l.err = ts, ""
		}
		x.mu.Unlock()
		close(done)
		x.h.Redraw()
	})
	return done
}

// refresh asks a tracker for its list again now: once more after the ask under way, if there is one.
func (x *Tickets) refresh(tr registry.TicketTracker) {
	x.stale(tr.Name)
	x.fresh(tr, true)
}

// stale marks a tracker's list to be asked for again.
func (x *Tickets) stale(name string) {
	x.mu.Lock()
	if l := x.lists[name]; l != nil {
		l.at = time.Time{}
	}
	x.mu.Unlock()
}

func (x *Tickets) refreshAll() {
	for _, tr := range registry.TicketTrackers(x.h) {
		x.refresh(tr)
	}
}

// listAt is when a tracker last answered; zero if it hasn't.
func (x *Tickets) listAt(name string) time.Time {
	x.mu.Lock()
	defer x.mu.Unlock()
	if l := x.lists[name]; l != nil && l.answered {
		return l.at
	}
	return time.Time{}
}

// listState is a tracker's list as the loading widgets read it.
func (x *Tickets) listState(name string) widget.LoadState {
	x.mu.Lock()
	defer x.mu.Unlock()
	l := x.lists[name]
	switch {
	case l == nil:
		return widget.LoadState{}
	case l.ask.Busy() != nil:
		return widget.LoadState{Phase: widget.Pending, Since: l.since, Stale: l.answered}
	case l.err != "" && l.tickets == nil:
		return widget.LoadState{Phase: widget.Failed, Err: errors.New(l.err)}
	}
	return widget.LoadState{Phase: widget.Ready}
}

// list is a tracker's tickets as last told, and why there are none.
func (x *Tickets) list(name string) ([]registry.Ticket, string, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	l := x.lists[name]
	if l == nil {
		return nil, "", false
	}
	return l.tickets, l.err, l.answered
}

// rows are the omnibox's tickets: each tracker's key ask or failure, then every tracker's tickets in one order.
func (x *Tickets) rows() []ticketRow {
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
		x.fresh(tr, false)
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

func (x *Tickets) source() uireg.PickerSource {
	return uireg.PickerSource{ID: "tickets", Title: "Tickets", Prefix: "!", Order: 40, Available: x.any,
		Loading: func() bool {
			for _, tr := range registry.TicketTrackers(x.h) {
				if tr.Assigned != nil && !x.needsKey(tr) && kit.PendingFirst(x.listState(tr.Name)) {
					return true
				}
			}
			return false
		},
		Items: func(registry.Ctx, string) []uireg.PickerItem {
			var out []uireg.PickerItem
			for _, r := range x.rows() {
				it := uireg.PickerItem{Icon: "◆", IconStyle: "status.permission", Data: r}
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
		Preview: func(it uireg.PickerItem) ui.Node {
			r, _ := it.Data.(ticketRow)
			return &ui.Func{Key: "ticket", Fn: func(c *ui.Ctx) {
				th := c.Theme()
				c.Column("p", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
					if r.action != "" {
						c.Text("t", it.Label, kit.UIFont(12, th.C(theme.FgMuted)))
						return
					}
					t := r.ticket
					tone := ui.TextStyle{Color: kit.TokenColour(th, stageTone(t.Stage), th.C(theme.Accent))}
					kit.PreviewHead(c, t.Key+" · "+strings.ToUpper(t.State), tone, t.Title)
					kit.PreviewField(c, "tracker", t.Tracker)
					if t.Project != "" {
						kit.PreviewField(c, "project", t.Project)
					}
					if t.PriorityLabel != "" {
						kit.PreviewField(c, "priority", t.PriorityLabel)
					}
					if t.PR != nil {
						kit.PreviewField(c, "pr", strings.TrimSpace(fmt.Sprintf("#%d %s", t.PR.Number, t.PR.Repo)))
					}
					if t.Blocked {
						kit.PreviewField(c, "blocked by", cmp.Or(strings.Join(t.BlockedBy, ", "), "another ticket"))
					}
					hint := "↵ starts work on it: a plan, the ticket its context"
					if se, ok := ticketSession(x.h, t.Key); ok {
						kit.PreviewField(c, "session", se.Name())
						hint = "↵ continues it in " + se.Name() + ", resumed if it's stopped or archived"
					}
					c.Text("hint", hint, kit.UIFont(11, th.C(theme.FgFaint)))
				})
			}}
		},
		Run: func(ctx registry.Ctx, it uireg.PickerItem) error {
			r, _ := it.Data.(ticketRow)
			switch r.action {
			case "key":
				return x.h.Run(ctx, "secrets.set", registry.Args{"name": r.tracker.Secret})
			case "refresh":
				x.refresh(r.tracker)
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
func (x *Tickets) moved(tr registry.TicketTracker, key string, m registry.TicketMove) {
	if !m.Moved {
		return
	}
	x.stale(tr.Name)
	x.h.Notify(core.Notice{ID: "tickets.moved", Severity: core.SevInfo, Source: tr.Source,
		Text: key + " moved to " + m.State + " in " + tr.Label()})
}

// trackerCall runs a call to a tracker off the loop, with a time limit; a failure goes to the log.
func (x *Tickets) trackerCall(tr registry.TicketTracker, what string, fn func(ctx context.Context) error) {
	x.work.Go(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if err := registry.Call(func() error { return fn(ctx) }); err != nil {
			x.h.Log().Warn("tickets: "+tr.Label()+" couldn't "+what, "err", err)
		}
	})
}

func (x *Tickets) Deactivate(ctx context.Context) error {
	x.work.Stop()
	return x.Disposer.Deactivate(ctx)
}

// started moves a session's ticket into progress as the session starts from it.
func (x *Tickets) started(ev kit.SessionStart) {
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
func (x *Tickets) prSeen(ev kit.PrSeen) {
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
func (x *Tickets) prMerged(ev kit.PrMerged) {
	if ev.Open > 0 {
		return
	}
	x.askDone(strings.TrimSpace(ev.Ticket), core.SessionID(ev.Session))
}

// askDoneID is the question asking whether to mark key done.
func askDoneID(key string) string { return "tickets.done:" + key }

// askDone asks whether a ticket is done, once its session's PRs are merged: it may need more of them, or something
// done by hand, so it's never moved without a yes.
func (x *Tickets) askDone(key string, session core.SessionID) {
	tr, ok := registry.TrackerFor(x.h, key)
	if !ok || tr.Done == nil {
		return
	}
	x.h.Notify(core.Notice{ID: askDoneID(key), Severity: core.SevQuestion, Source: tr.Source, Session: session,
		Title: "Mark " + key + " done in " + tr.Label() + "?",
		Text:  "The session's PRs are merged. If the ticket needs nothing more (more PRs, something done by hand), mark it done.",
		Actions: []core.Action{
			{Title: "Mark done", Command: "tickets.done", Args: map[string]any{"ticket": key}},
			{Title: "Not yet", Command: "notice.dismiss", Args: map[string]any{"id": askDoneID(key)}},
		}})
}

// markDone moves a ticket to its tracker's done, as the user asked; a failure is shown, not only logged.
func (x *Tickets) markDone(key string) error {
	tr, ok := registry.TrackerFor(x.h, key)
	if !ok || tr.Done == nil {
		return fmt.Errorf("no ticket tracker can mark %s done", key)
	}
	x.h.Dismiss(askDoneID(key))
	x.trackerCall(tr, "move "+key+" to done", func(ctx context.Context) error {
		m, err := tr.Done(ctx, key)
		if err != nil {
			x.h.Notify(core.Notice{ID: "tickets.done-failed", Severity: core.SevError, Source: tr.Source,
				Text: key + " couldn't be marked done in " + tr.Label() + ": " + err.Error()})
			return err
		}
		x.moved(tr, key, m)
		return nil
	})
	return nil
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
func (x *Tickets) toolMyTickets(ctx registry.Ctx, _ map[string]any) (any, error) {
	trackers := registry.TicketTrackers(x.h)
	var waits []<-chan struct{}
	for _, tr := range trackers {
		if w := x.fresh(tr, false); w != nil {
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
