package sessions

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The tickets page: your assigned tickets from every tracker, or one, on the left, the
// selected one on the right with Plan, Work and Open, over the main area beside the sidebar, as Jobs is laid out.

// TicketsKeyMode is the tickets page's keymap mode.
const TicketsKeyMode = "tickets"

const ticketsHelp = "agent/tickets"

// ticketsPage is the page's state.
type ticketsPage struct {
	open        bool
	tracker     string // the one tracker it lists; every tracker's when ""
	filter      string
	focusFilter bool
	sel         string // the selected ticket's key
	err         string
}

// ticketDetail is a ticket as its tracker's lookup told it.
type ticketDetail struct {
	t     registry.Ticket
	err   string
	done  bool
	since time.Time
}

func (x *Tickets) openPage(tracker string) error {
	tracker = strings.TrimSpace(tracker)
	if tracker != "" && !slices.ContainsFunc(registry.TicketTrackers(x.h), func(tr registry.TicketTracker) bool { return tr.Name == tracker }) {
		return fmt.Errorf("no ticket tracker %q", tracker)
	}
	x.page = ticketsPage{open: true, tracker: tracker}
	return nil
}

// pageTrackers are the trackers the page lists.
func (x *Tickets) pageTrackers() []registry.TicketTracker {
	return slices.DeleteFunc(registry.TicketTrackers(x.h), func(tr registry.TicketTracker) bool {
		return tr.Assigned == nil || x.page.tracker != "" && tr.Name != x.page.tracker
	})
}

// pageTitle is "Linear tickets" for one tracker, else "Tickets".
func (x *Tickets) pageTitle() string {
	if ts := x.pageTrackers(); x.page.tracker != "" && len(ts) == 1 {
		return ts[0].Label() + " tickets"
	}
	return "Tickets"
}

// pageRows are the page's rows: rows() narrowed to its tracker, then to the filter.
func (x *Tickets) pageRows() []ticketRow {
	q := strings.ToLower(strings.TrimSpace(x.page.filter))
	return slices.DeleteFunc(x.rows(), func(r ticketRow) bool {
		if x.page.tracker != "" && r.tracker.Name != x.page.tracker {
			return true
		}
		if r.ticket == nil || q == "" {
			return false
		}
		t := r.ticket
		return !strings.Contains(strings.ToLower(t.Key+" "+t.Title+" "+t.Project), q)
	})
}

// detail is the selected ticket in full, asked of its tracker once, off the loop.
func (x *Tickets) detail(tr registry.TicketTracker, key string) *ticketDetail {
	x.mu.Lock()
	defer x.mu.Unlock()
	if d := x.details[key]; d != nil {
		return d
	}
	d := &ticketDetail{since: kit.WindowNow(x.h)}
	x.details[key] = d
	x.work.Go(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		t, err := registry.LookupTicket(ctx, tr, key)
		x.mu.Lock()
		d.t, d.done = t, true
		if err != nil {
			d.err = err.Error()
		}
		x.mu.Unlock()
		x.h.Redraw()
	})
	return d
}

// selectedRow is the row whose ticket is selected, else the first ticket's.
func (x *Tickets) selectedRow(rows []ticketRow) (ticketRow, bool) {
	first := -1
	for i, r := range rows {
		if r.ticket == nil {
			continue
		}
		if r.ticket.Key == x.page.sel {
			return r, true
		}
		if first < 0 {
			first = i
		}
	}
	if first < 0 {
		return ticketRow{}, false
	}
	return rows[first], true
}

// act runs what a row does: a ticket's Plan or Work, a tracker's key or retry. resume continues the ticket's session
// instead, when it has one.
func (x *Tickets) act(r ticketRow, kind core.Kind, resume bool) {
	run := func(id string, a registry.Args) {
		if err := x.h.Run(context.Background(), id, a); err != nil {
			x.page.err = err.Error()
			return
		}
		x.page.open = false
	}
	switch r.action {
	case "key":
		if err := x.h.Run(context.Background(), "secrets.set", registry.Args{"name": r.tracker.Secret}); err != nil {
			x.page.err = err.Error()
		}
		return
	case "refresh":
		x.refresh(r.tracker)
		return
	}
	if se, ok := ticketSession(x.h, r.ticket.Key); ok && resume {
		run("session.open", registry.Args{"id": string(se.ID())})
		return
	}
	run("session.new", registry.Args{"kind": string(kind), "ticket": r.ticket.Key})
}

// browse opens the selected ticket in its tracker, in the browser.
func (x *Tickets) browse(r ticketRow) {
	if r.ticket == nil || r.ticket.URL == "" {
		return
	}
	if err := x.h.Run(context.Background(), "system.open", registry.Args{"url": r.ticket.URL}); err != nil {
		x.page.err = err.Error()
	}
}

// pageCommand runs one of the page's keys on the selected ticket.
func (x *Tickets) pageCommand(what string) error {
	r, ok := x.selectedRow(x.pageRows())
	if !x.page.open || !ok {
		return nil
	}
	switch what {
	case "plan":
		x.act(r, core.KindPlan, true)
	case "work":
		x.act(r, core.KindWork, false)
	case "browse":
		x.browse(r)
	}
	return nil
}

func (x *Tickets) pageView(c *ui.Ctx) {
	if !x.page.open || c.Theme().IsProbe() {
		return
	}
	rows := x.pageRows()
	widget.Page(c, "tickets", &x.page.open, widget.PageOpts{KeyMode: TicketsKeyMode, Label: x.pageTitle() + " page", Help: ticketsHelp},
		func(c *ui.Ctx) {
			th := c.Theme()
			x.pageHeader(c)
			c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				x.pageList(c, rows)
				c.Box("divider", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
				c.Column("detail", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 18, R: 18, T: 14, B: 18}, Gap: 12,
					Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
					if r, ok := x.selectedRow(rows); ok {
						x.pageDetail(c, r)
					}
				})
			})
		})
}

func (x *Tickets) pageHeader(c *ui.Ctx) {
	th := c.Theme()
	c.Row("top", ui.Style{W: ui.Flex(1), H: ui.Px(42), Pad: ui.PadXY(18, 0), Gap: 12, Cross: ui.Center, Fill: th.C(kit.TStatsHead)}, func(c *ui.Ctx) {
		c.Text("h", strings.ToUpper(x.pageTitle()), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.SemiBold, Tracking: .04},
			Color: th.C(theme.Fg)})
		n := 0
		var at time.Time
		for _, tr := range x.pageTrackers() {
			ts, _, _ := x.list(tr.Name)
			n += len(ts)
			if t := x.listAt(tr.Name); !t.IsZero() && (at.IsZero() || t.Before(at)) {
				at = t
			}
		}
		meta := []widget.AgoPart{{Text: fmt.Sprintf("%d assigned", n)}}
		if !at.IsZero() {
			meta = append(meta, widget.AgoPart{Text: "updated ", At: at, Long: true})
		}
		widget.AgoLine(c, "m", widget.AgoLineOpts{Text: kit.MonoFont(11, th.C(theme.FgFaint))}, meta...)
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if x.page.err != "" {
			c.Text("err", x.page.err, kit.MonoFont(11, th.C(theme.Error)))
		}
		if widget.Button(c, "refresh", "Refresh", widget.ButtonOpts{Key: "r", Busy: x.pageState().Phase == widget.Pending, BusyLabel: "Refreshing",
			Help: ticketsHelp + "#page"}) {
			x.refreshAll()
		}
		if widget.Button(c, "close", "✕", widget.ButtonOpts{Key: "esc", Help: ticketsHelp + "#page"}) {
			x.page.open = false
		}
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

// pageList is the tickets, filtered, a row each, after what a tracker needs first.
func (x *Tickets) pageList(c *ui.Ctx, rows []ticketRow) {
	th := c.Theme()
	c.Column("list", ui.Style{W: ui.Px(420), H: ui.Flex(1), Fill: th.C(theme.BgSurface)}, func(c *ui.Ctx) {
		c.Row("filter", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, T: 10, B: 6}}, func(c *ui.Ctx) {
			if x.page.focusFilter {
				c.RequestFocus(c.Next("q"))
				x.page.focusFilter = false
			}
			if r := widget.Input(c, "q", &x.page.filter, widget.InputOpts{Label: "Filter tickets", Placeholder: "⌕ filter by key, title, project",
				Help: ticketsHelp + "#list"}); r.Cancelled {
				x.page.filter = ""
			}
		})
		c.Column("rows", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true}, func(c *ui.Ctx) {
			x.pageRowsView(c, rows)
		})
	})
}

// pageRowsView is the list's rows: skeleton rows while the trackers first answer, then the tickets or why there are
// none, with a refresh bar while they're asked again.
func (x *Tickets) pageRowsView(c *ui.Ctx, rows []ticketRow) {
	empty := len(rows) == 0
	pad := ui.Insets{}
	if empty || x.pageState().Loading() {
		pad = ui.PadXY(14, 12)
	}
	c.Column("load", ui.Style{W: ui.Flex(1), Pad: pad}, func(c *ui.Ctx) {
		widget.Async(c, "load", x.pageState(), widget.AsyncOpts{Label: "Asking " + x.trackerNames() + " for your tickets", Empty: x.emptyText(),
			IsEmpty: empty, Width: 380, Help: ticketsHelp + "#list", Loading: func(c *ui.Ctx) {
				widget.Skeleton(c, "rows", widget.SkeletonOpts{Rows: 5, RowH: 14, Gap: 16, Widths: []float32{.9, .6, .8}, Label: "Tickets",
					Help: ticketsHelp + "#list"})
			}}, func(c *ui.Ctx) {
			sel := 0
			if r, ok := x.selectedRow(rows); ok {
				sel = slices.IndexFunc(rows, func(o ticketRow) bool { return o.ticket == r.ticket })
			}
			many := len(x.pageTrackers()) > 1
			res := widget.Rows(c, "tickets", len(rows), &sel, widget.RowsOpts{Label: "Tickets", KeyMode: TicketsKeyMode, Help: ticketsHelp + "#list",
				ClickSelects: true, Static: func(i int) bool { return rows[i].ticket == nil }, RowLabel: func(i int) string { return rowLabel(rows[i]) }},
				func(c *ui.Ctx, i int, _ bool) { x.ticketRowView(c, rows[i], many) })
			if sel >= 0 && sel < len(rows) && rows[sel].ticket != nil && (res.Changed || x.page.sel == "") {
				x.page.sel = rows[sel].ticket.Key
			}
			if res.Activated >= 0 {
				x.act(rows[res.Activated], core.KindPlan, true)
			}
		})
	})
}

// pageState is the page's trackers' lists read together: loading while any hasn't answered yet, refreshing while
// any is asked again. A tracker that failed is a row of its own, with Retry.
func (x *Tickets) pageState() widget.LoadState {
	out := widget.LoadState{Phase: widget.Ready}
	for _, tr := range x.pageTrackers() {
		if x.needsKey(tr) {
			continue
		}
		s := x.listState(tr.Name)
		switch {
		case s.Phase == widget.Idle || s.Loading():
			if out.Phase != widget.Pending || out.Stale || s.Since.Before(out.Since) {
				out = widget.LoadState{Phase: widget.Pending, Since: s.Since}
			}
		case s.Phase == widget.Pending && out.Phase == widget.Ready:
			out = widget.LoadState{Phase: widget.Pending, Since: s.Since, Stale: true}
		}
	}
	return out
}

// trackerNames are the page's trackers by name: "Linear or Jira".
func (x *Tickets) trackerNames() string {
	var names []string
	for _, tr := range x.pageTrackers() {
		names = append(names, tr.Label())
	}
	return cmp.Or(strings.Join(names, " or "), "any tracker")
}

// emptyText is what the list says once the trackers have answered with nothing.
func (x *Tickets) emptyText() string {
	if strings.TrimSpace(x.page.filter) != "" {
		return "No ticket matches."
	}
	return "Nothing assigned to you in " + x.trackerNames()
}

// rowLabel is a row's accessible name: the ticket's key and title, or what the tracker needs.
func rowLabel(r ticketRow) string {
	switch r.action {
	case "key":
		return "Set your " + r.tracker.Label() + " API key"
	case "refresh":
		return r.tracker.Label() + ": " + r.err
	}
	return r.ticket.Key + " " + r.ticket.Title
}

func (x *Tickets) ticketRowView(c *ui.Ctx, r ticketRow, many bool) {
	th := c.Theme()
	on := r.ticket != nil && r.ticket.Key == x.page.sel
	fill, edge := render.Color{}, render.Color{}
	if on {
		fill, edge = th.C(kit.TStatsRowOn), th.C(theme.Accent)
	}
	c.Row("row", ui.Style{W: ui.Flex(1), Fill: fill}, func(c *ui.Ctx) {
		c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
		c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 14, T: 9, B: 9}, Gap: 5, Clip: true}, func(c *ui.Ctx) {
			if r.ticket == nil {
				tone, note, button := th.C(theme.Accent), "to list the tickets assigned to you", "Set key"
				if r.action == "refresh" {
					tone, note, button = th.C(theme.Warn), "it didn't answer", "Retry"
				}
				c.Row("l1", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Column("t", ui.Style{W: ui.Flex(1), Gap: 4, Clip: true}, func(c *ui.Ctx) {
						c.Text("t", rowLabel(r), kit.UIFont(13, tone))
						c.Text("n", note, kit.MonoFont(10, th.C(theme.FgFaint)))
					})
					if widget.Button(c, "act", button, widget.ButtonOpts{Help: ticketsHelp + "#list"}) {
						x.act(r, core.KindPlan, true)
					}
				})
				return
			}
			t := r.ticket
			c.Row("l1", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				if many && r.tracker.Icon != "" {
					kit.Icon(c, x.h, "tracker", r.tracker.Icon, 12, th.C(theme.FgMuted))
				}
				c.Text("k", t.Key, kit.MonoFont(11, th.C(theme.FgMuted)))
				c.Text("t", t.Title, kit.UIFont(13, th.C(theme.Fg)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				if t.State != "" {
					widget.Chip(c, "st", t.State, kit.TokenColour(th, stageTone(t.Stage), th.C(theme.Accent)), ticketsHelp+"#list")
				}
			})
			c.Row("l2", ui.Style{W: ui.Flex(1), Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
				var parts []string
				if t.PriorityLabel != "" {
					parts = append(parts, strings.ToLower(t.PriorityLabel))
				}
				if t.Project != "" {
					parts = append(parts, t.Project)
				}
				if t.PR != nil {
					parts = append(parts, fmt.Sprintf("PR #%d", t.PR.Number))
				}
				c.Text("n", strings.Join(parts, " · "), kit.MonoFont(10, th.C(theme.FgFaint)))
				if t.Blocked {
					c.Text("b", "blocked", kit.MonoFont(10, th.C(theme.Warn)))
				}
				if se, ok := ticketSession(x.h, t.Key); ok {
					c.Text("se", continueLabel(se), kit.MonoFont(10, th.C(theme.Accent)))
				}
			})
		})
	})
}

// pageDetail is the selected ticket: the list's fields at once, then its tracker's detail, and what to do with it.
func (x *Tickets) pageDetail(c *ui.Ctx, r ticketRow) {
	th := c.Theme()
	t := r.ticket
	d := x.detail(r.tracker, t.Key)
	x.mu.Lock()
	full, failed, done := d.t, d.err, d.done
	x.mu.Unlock()
	help := ticketsHelp + "#ticket"
	c.Row("kicker", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("k", t.Key, kit.MonoFont(11, th.C(theme.FgMuted)))
		if t.State != "" {
			widget.Chip(c, "st", t.State, kit.TokenColour(th, stageTone(t.Stage), th.C(theme.Accent)), help)
		}
	})
	widget.Wrap(c, "title", t.Title, 640, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 17, Weight: typeset.SemiBold},
		Color: th.C(theme.Fg)})
	c.Row("acts", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		plan := "Plan"
		if se, ok := ticketSession(x.h, t.Key); ok {
			plan = continueLabel(se)
		}
		if widget.Button(c, "plan", plan, widget.ButtonOpts{Primary: true, Key: "↵", Help: help}) {
			x.act(r, core.KindPlan, true)
		}
		if widget.Button(c, "work", "Work", widget.ButtonOpts{Key: "w", Help: help}) {
			x.act(r, core.KindWork, false)
		}
		if t.URL != "" && widget.Button(c, "open", "Open in "+r.tracker.Label(), widget.ButtonOpts{Ghost: true, Key: "o", Help: help}) {
			x.browse(r)
		}
	})
	c.Column("fields", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		kit.PreviewField(c, "tracker", r.tracker.Label())
		if t.Project != "" {
			kit.PreviewField(c, "project", t.Project)
		}
		if t.PriorityLabel != "" {
			kit.PreviewField(c, "priority", t.PriorityLabel)
		}
		if t.PR != nil {
			kit.PreviewField(c, "pr", strings.TrimSpace(fmt.Sprintf("#%d %s", t.PR.Number, t.PR.Repo)))
		}
		blockedBy := t.BlockedBy
		if done && len(full.BlockedBy) > 0 {
			blockedBy = full.BlockedBy
		}
		if t.Blocked || len(blockedBy) > 0 {
			kit.PreviewField(c, "blocked by", cmp.Or(strings.Join(blockedBy, ", "), "another ticket"))
		}
		if se, ok := ticketSession(x.h, t.Key); ok {
			kit.PreviewField(c, "session", se.Name())
		}
	})
	switch {
	case !done:
		widget.Busy(c, "asking", widget.LoadState{Phase: widget.Pending, Since: d.since}, widget.BusyOpts{Label: "Asking " + r.tracker.Label() + " for the rest",
			Size: 11, Help: help})
	case failed != "":
		widget.Wrap(c, "err", failed, 640, kit.MonoFont(11, th.C(theme.Error)))
	default:
		if len(full.Facts) > 0 {
			c.Text("facts", strings.Join(full.Facts, " · "), kit.MonoFont(11, th.C(theme.FgFaint)))
		}
		switch {
		case full.Description != "":
			kit.MarkdownText(c, x.h, "description", full.Description, 640, kit.UIFont(13, th.C(theme.FgMuted)), kit.MarkdownOpts{})
		case full.Summary != "":
			widget.Wrap(c, "summary", full.Summary, 640, kit.UIFont(13, th.C(theme.FgMuted)))
		}
	}
}
