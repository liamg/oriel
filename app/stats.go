package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// Stats and Usage (AI-12, mockup 43): the session stats tab, the global Stats page and the Usage page, built on what
// the session host answers (StatsQuery, SessionStats, UsageHistory), and the ways to them: the sidebar's global
// entries, the title bar's indicators, the session stats box's cost, the start view's links and the omnibox.
const (
	StatsSessionView = "stats.session"
	StatsKeyMode     = "stats"
	UsageKeyMode     = "usage"
	SplitOption      = "stats.split_sessions"
	AccountsOption   = "usage.accounts"
)

// statsRanges are the page's ranges, in days; 0 is a custom one.
var statsRanges = []statsRange{{"7D", 7}, {"30D", 30}, {"90D", 90}, {"CUSTOM", 0}}

type statsRange struct {
	label string
	days  int
}

type statsGroup struct{ key, label, short string }

var statsGroups = []statsGroup{
	{stats.ByRepo, "REPO", "r"}, {stats.BySession, "SESSION", "s"}, {stats.ByKind, "KIND", "k"}, {stats.ByModel, "MODEL", "m"},
	{stats.ByProvider, "PROVIDER", "p"}, {stats.ByAccount, "ACCOUNT", "a"}, {stats.ByOrigin, "ORIGIN", "o"}, {stats.ByDay, "DAY", "d"},
}

// statsOrigins are the origin filter's choices: every session, Oriel's, or those outside it.
var statsOrigins = []struct{ label, value string }{{"ALL", ""}, {"ORIEL", stats.OriginOriel}, {"OUTSIDE", stats.OriginOutside}}

// refetchAfter is how old an answer gets before it's asked for again while it's shown.
const refetchAfter = time.Minute

// statsQuery is what the Stats page shows: a range, a grouping and filters.
type statsQuery struct {
	rng      int // into statsRanges
	from, to time.Time
	group    string
	filters  map[string][]string
}

func (q statsQuery) clone() statsQuery {
	q.filters = cloneFilters(q.filters)
	return q
}

func cloneFilters(f map[string][]string) map[string][]string {
	out := map[string][]string{}
	for k, v := range f {
		out[k] = slices.Clone(v)
	}
	return out
}

// fetched is the session host's last answer.
type fetched[T any] struct {
	v   T
	at  time.Time
	err error
}

type statsPlugin struct {
	disposer
	sh  *shell
	h   plugin.Host
	now func() time.Time
	loc *time.Location

	open    bool       // the Stats page
	picking bool       // its custom range's date picker is open
	q       statsQuery // what it shows
	back    []statsQuery
	sel     int // the table's selected row
	hover   int // the row or series under the pointer last frame, -1
	// hovering is the one under it this frame.
	hovering int

	usageOpen bool
	acct      string         // the Usage page's account, provider and identity (accountKey); "" for the session in front's
	burnWin   map[string]int // the Usage page's window picked for "burned the window", by account

	mu       sync.Mutex
	reports  map[string]fetched[stats.Report] // by query
	details  map[string]fetched[stats.Detail] // by session and split
	history  fetched[[]core.ProviderUsage]
	inflight map[string]time.Time   // when each fetch running started
	live     []core.ProviderUsage   // readings since the history was fetched, from UsageSampled
	reading  *plugin.Load[struct{}] // Refresh's read of every provider's windows
	changed  time.Time              // when an agent last did something that costs: answers fetched before are stale
}

// liveAfter is how soon a report is asked for again once the agents it counts have moved on.
const liveAfter = 10 * time.Second

func (x *statsPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "stats"} }

func (x *statsPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h, x.sh.stats = h, x
	if x.now == nil {
		x.now = time.Now
	}
	if x.loc == nil {
		x.loc = time.Local
	}
	x.q = statsQuery{rng: 1, group: stats.ByRepo, filters: map[string][]string{}}
	x.hover = -1
	x.burnWin = map[string]int{}
	x.reports = map[string]fetched[stats.Report]{}
	x.details = map[string]fetched[stats.Detail]{}
	x.inflight = map[string]time.Time{}
	x.reading = plugin.NewLoad(h, func(ctx context.Context, _ func(string, float64)) (struct{}, error) {
		w := x.sh.cfg.Host
		if w == nil {
			return struct{}{}, errors.New("there are no sessions in this window")
		}
		err := w.ReadUsage(ctx)
		x.h.Do(func() {
			x.mu.Lock()
			x.history = fetched[[]core.ProviderUsage]{}
			x.mu.Unlock()
		})
		return struct{}{}, err
	}, plugin.LoadOpts{Timeout: 2 * time.Minute, Now: func() time.Time { return x.now() }})
	x.add(h.Option(registry.OptionSpec{Name: SplitOption, Type: registry.Bool, Default: true, Scopes: registry.ScopeUser,
		Help: "Split a multi-repo session's cost over its repos by each one's share of edits and file tool calls. Off, each repo is charged the whole session. The Stats page's toggle and the session tab's \"count whole session in each\" set it."}))
	x.add(h.Option(registry.OptionSpec{Name: AccountsOption, Type: registry.Map, Default: map[string]string{}, Scopes: registry.ScopeUser,
		KeyLabel: "account", ValueLabel: "label",
		Help: "Names for the accounts plans belong to, by identity: \"Work\", \"Personal\". Usage and Stats show them in place of the account's email or organisation."}))
	has := func(registry.Args) bool { return x.sh.sessions != nil && x.sh.cfg.Host != nil }
	for _, c := range []registry.Command{
		{ID: "Stats", Title: "The Stats page; :Stats {repo=api 7d session group=model}", Run: func(ctx registry.Ctx, a registry.Args) error {
			return x.ex(ctx, a.String("args"))
		}},
		{ID: "stats.open", Title: "Open the Stats page: {group} by, a {range} (7d, 30d, 90d), filtered by {repo}, {provider}, {account}, {session}, {kind}, {model}, {origin} (oriel, outside)",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.openPage(a) }},
		{ID: "stats.session", Title: "Open a session's stats as a main tab: {id}, the one in front by default, or one outside Oriel",
			Available: has, Run: func(ctx registry.Ctx, a registry.Args) error {
				return x.openSession(ctx, core.SessionID(a.String("id")))
			}},
		{ID: "stats.origin", Title: "Show the Stats page's sessions from {origin}: oriel, outside, or \"\" for every one", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.setOrigin(a.String("origin"))
		}},
		{ID: "stats.group", Title: "Group the Stats page {by} repo, session, kind, model, provider, account, origin or day", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.regroup(a.String("by"))
		}},
		{ID: "stats.range", Title: "Step the Stats page's range {step} -1 shorter or 1 longer", Run: func(_ registry.Ctx, a registry.Args) error {
			x.q.rng = min(max(x.q.rng+a.Int("step"), 0), len(statsRanges)-2)
			return nil
		}},
		{ID: "stats.custom", Title: "Set the Stats page's custom range {range} (2026-09-01..2026-09-30), or pick it in the date picker",
			Run: func(_ registry.Ctx, a registry.Args) error {
				if a.String("range") == "" {
					x.open, x.usageOpen, x.picking = true, false, true
					return nil
				}
				r, err := widget.ParseDateRange(a.String("range"), x.loc)
				if err != nil {
					return err
				}
				x.open, x.usageOpen = true, false
				x.custom(r)
				return nil
			}},
		{ID: "stats.move", Title: "Move the Stats page's selection {by} rows", Run: func(_ registry.Ctx, a registry.Args) error {
			x.sel = max(x.sel+a.Int("by"), 0)
			return nil
		}},
		{ID: "stats.drill", Title: "Drill into the Stats page's selected row: its filter chip, regrouped", Run: func(registry.Ctx, registry.Args) error {
			return x.drillSelected()
		}},
		{ID: "stats.back", Title: "Back out of the Stats page's last drill", Run: func(registry.Ctx, registry.Args) error {
			if n := len(x.back); n > 0 {
				x.q, x.back = x.back[n-1], x.back[:n-1]
			}
			return nil
		}},
		{ID: "stats.copy", Title: "Copy the Stats page's table as CSV", Run: func(registry.Ctx, registry.Args) error {
			x.sh.desktop.SetClipboard(x.csv())
			return nil
		}},
		{ID: "stats.split", Title: "Split multi-repo sessions over their repos: {on}, or switch", Run: func(ctx registry.Ctx, a registry.Args) error {
			on, ok := a["on"].(bool)
			if !ok {
				on = !x.split()
			}
			return x.setSplit(ctx, on)
		}},
		{ID: "Usage", Title: "The Usage page: each provider's plan windows", Run: func(registry.Ctx, registry.Args) error { return x.openUsage() }},
		{ID: "usage.open", Title: "Open the Usage page: each provider's plan windows", Run: func(registry.Ctx, registry.Args) error { return x.openUsage() }},
		{ID: "usage.refresh", Title: "Read every provider's plan windows again", Available: has, Run: func(ctx registry.Ctx, _ registry.Args) error {
			return x.refreshUsage(ctx)
		}},
	} {
		x.add(h.Command(c))
	}
	x.add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-S>", Command: "stats.open", Desc: "Stats"}))
	x.add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-U>", Command: "usage.open", Desc: "Usage"}))
	for _, g := range statsGroups {
		x.add(h.Keymap(registry.Keymap{Mode: StatsKeyMode, Keys: "g" + g.short, Command: "stats.group", Args: registry.Args{"by": g.key},
			Desc: "Group by " + g.key}))
	}
	x.add(h.Keymap(registry.Keymap{Mode: StatsKeyMode, Keys: "g", Desc: "+group by"}))
	x.add(h.Keymap(registry.Keymap{Mode: StatsKeyMode, Keys: "c", Command: "stats.custom", Desc: "Custom range"}))
	for _, k := range []registry.Keymap{
		{Keys: "[", Command: "stats.range", Args: registry.Args{"step": -1}, Desc: "Shorter range"},
		{Keys: "]", Command: "stats.range", Args: registry.Args{"step": 1}, Desc: "Longer range"},
		{Keys: "j", Command: "stats.move", Args: registry.Args{"by": 1}}, {Keys: "<Down>", Command: "stats.move", Args: registry.Args{"by": 1}},
		{Keys: "k", Command: "stats.move", Args: registry.Args{"by": -1}}, {Keys: "<Up>", Command: "stats.move", Args: registry.Args{"by": -1}},
		{Keys: "<CR>", Command: "stats.drill", Desc: "Drill in"},
		{Keys: "<BS>", Command: "stats.back", Desc: "Back out"},
	} {
		k.Mode = StatsKeyMode
		x.add(h.Keymap(k))
	}
	x.add(h.Keymap(registry.Keymap{Mode: UsageKeyMode, Keys: "r", Command: "usage.refresh", Desc: "Read the windows again"}))
	for _, e := range []registry.RailEntry{
		{Name: "stats", Icon: "stats", Title: "Stats", Command: "stats.open", Scope: registry.ScopeGlobalEntry, Order: 10,
			Note: func(string) string { return x.figure("today") }},
		{Name: "usage", Icon: "usage", Title: "Usage", Command: "usage.open", Scope: registry.ScopeGlobalEntry, Order: 20,
			Note: func(string) string { return x.usageFigure() }},
	} {
		x.add(h.Contribute(e))
	}
	for _, e := range []registry.StartEntry{
		{Name: "stats", Icon: "stats", Title: "Stats", Desc: "What the agents cost and did", Command: "stats.open", Order: 60, Link: true,
			Note: func() string { return x.figure("week") }},
		{Name: "usage", Icon: "usage", Title: "Usage", Desc: "Each provider's plan windows", Command: "usage.open", Order: 70, Link: true,
			Note: x.usageFigure},
	} {
		x.add(h.Contribute(e))
	}
	x.add(onDispose(h.Subscribe(core.Filter{}, func(ev core.Event) {
		switch ev := ev.(type) {
		case core.UsageSampled:
			x.mu.Lock()
			x.live = append(x.live, ev.Usage)
			if n := len(x.live); n > 500 {
				x.live = x.live[n-500:]
			}
			x.mu.Unlock()
		case core.AgentChanged, core.TranscriptAppended, core.ThreadUpdated, core.SessionOpened:
			x.mu.Lock()
			x.changed = x.now()
			x.mu.Unlock()
		}
	})))
	x.add(h.Picker(pagesSource(h)))
	x.add(h.View(StatsSessionView, sessionStatsView{x}))
	x.add(h.View(OverlayPrefix+"stats", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "stats", Fn: x.page} })))
	x.add(h.View(OverlayPrefix+"usage", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "usage", Fn: x.usagePage} })))
	x.add(h.Contribute(registry.Indicator{Name: "usage", Slot: registry.SlotHUD, Order: 10, Title: "Plan usage", Help: hudHelp + "#usage",
		Note: x.usageNote, Build: func(registry.Ctx) (ui.Node, error) { return &ui.Func{Key: "usage", Fn: x.usagePanel}, nil }}))
	x.add(h.Contribute(registry.Indicator{Name: "today", Slot: registry.SlotHUD, Order: 40, Title: "Today", Help: hudHelp + "#today",
		Note: func() string { return "since 00:00" }, Build: func(registry.Ctx) (ui.Node, error) { return &ui.Func{Key: "today", Fn: x.todayPanel}, nil }}))
	x.add(h.Contribute(registry.Indicator{Name: "usage.mission", Slot: registry.SlotMission, Order: 10, Help: missionHelp + "#strip",
		Build: func(registry.Ctx) (ui.Node, error) { return &ui.Func{Key: "usage", Fn: x.usageStrip}, nil }}))
	x.add(h.View(TitleRight+"40-usage", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "usage", Fn: x.indicators} })))
	return nil
}

// split is stats.split_sessions.
func (x *statsPlugin) split() bool {
	v, _, _ := x.h.Options().Get(SplitOption, registry.At{})
	on, ok := v.(bool)
	return on || !ok
}

func (x *statsPlugin) setSplit(ctx registry.Ctx, on bool) error {
	if x.h.Available("settings.set", nil) {
		return x.h.Run(ctx, "settings.set", registry.Args{"name": SplitOption, "value": on, "scope": "user"})
	}
	return x.h.Options().Set(SplitOption, registry.LayerUser, registry.At{}, on)
}

// ex runs :Stats with its words: "session" for the session in front's tab, else a page with key=value filters, a
// range (7d) and a group.
func (x *statsPlugin) ex(ctx registry.Ctx, args string) error {
	a := registry.Args{}
	for _, w := range strings.Fields(args) {
		switch k, v, ok := strings.Cut(w, "="); {
		case w == "session" || w == "session=":
			return x.openSession(ctx, "")
		case ok && (k == "group" || k == "by"):
			a["group"] = v
		case ok:
			a[k] = v
		default:
			a["range"] = w
		}
	}
	return x.openPage(a)
}

func (x *statsPlugin) openPage(a registry.Args) error {
	q := statsQuery{rng: x.q.rng, group: cmpOr(a.String("group"), stats.ByRepo), filters: map[string][]string{}}
	if r := strings.ToUpper(a.String("range")); r != "" {
		i := slices.IndexFunc(statsRanges, func(s statsRange) bool { return s.label == r })
		days, err := widget.ParseDateRange(r, x.loc)
		switch {
		case i >= 0:
			q.rng = i
		case err == nil:
			q.rng, q.from, q.to = len(statsRanges)-1, days.From, days.To.AddDate(0, 0, 1)
		default:
			return fmt.Errorf("a range is 7d, 30d, 90d or 2026-09-01..2026-09-30, not %q", a.String("range"))
		}
	}
	if !slices.ContainsFunc(statsGroups, func(g statsGroup) bool { return g.key == q.group }) {
		return fmt.Errorf("no grouping %q: repo, session, kind, model, provider, account, origin or day", q.group)
	}
	for _, k := range []string{"repo", "provider", "account", "session", "kind", "model", "origin"} {
		switch v := a[k].(type) {
		case string:
			if v != "" {
				q.filters[k] = strings.Split(v, ",")
			}
		case []any:
			for _, s := range v {
				q.filters[k] = append(q.filters[k], fmt.Sprint(s))
			}
		case []string:
			q.filters[k] = v
		}
	}
	x.q, x.back, x.sel, x.hover = q, nil, 0, -1
	x.open, x.usageOpen = true, false
	return nil
}

// custom makes the page's range r's days.
func (x *statsPlugin) custom(r widget.DateRange) {
	x.q.rng, x.q.from, x.q.to, x.sel = len(statsRanges)-1, r.From, r.To.AddDate(0, 0, 1), 0
}

func (x *statsPlugin) openUsage() error {
	x.usageOpen, x.open = true, false
	return nil
}

func (x *statsPlugin) regroup(by string) error {
	if !slices.ContainsFunc(statsGroups, func(g statsGroup) bool { return g.key == by }) {
		return fmt.Errorf("no grouping %q: repo, session, kind, model, provider, account, origin or day", by)
	}
	x.q.group, x.sel = by, 0
	return nil
}

// setOrigin keeps the page to one origin's sessions, or every one's with "".
func (x *statsPlugin) setOrigin(origin string) error {
	switch origin {
	case "":
		delete(x.q.filters, "origin")
	case stats.OriginOriel, stats.OriginOutside:
		x.q.filters["origin"] = []string{origin}
	default:
		return fmt.Errorf("no origin %q: oriel or outside", origin)
	}
	x.sel = 0
	return nil
}

// drillOrder is what a group's rows regroup by when drilled into.
var drillOrder = map[string]string{stats.ByRepo: stats.BySession, stats.BySession: stats.ByModel, stats.ByKind: stats.BySession,
	stats.ByModel: stats.BySession, stats.ByProvider: stats.ByModel, stats.ByAccount: stats.BySession, stats.ByOrigin: stats.BySession,
	stats.ByDay: stats.BySession}

// drill narrows the page to a group's row: its filter chip, regrouped. A day becomes the range.
func (x *statsPlugin) drill(g stats.Group) {
	x.back = append(x.back, x.q.clone())
	q := x.q.clone()
	if q.group == stats.ByDay {
		day, err := time.ParseInLocation("2006-01-02", g.Key, x.loc)
		if err == nil {
			q.rng, q.from, q.to = len(statsRanges)-1, day, day.AddDate(0, 0, 1)
		}
	} else if !slices.Contains(q.filters[q.group], g.Key) {
		q.filters[q.group] = []string{g.Key}
	}
	q.group = drillOrder[q.group]
	x.q, x.sel, x.hover = q, 0, -1
}

func (x *statsPlugin) drillSelected() error {
	r, _ := x.report(x.query())
	if x.sel >= 0 && x.sel < len(r.Groups) {
		x.drill(r.Groups[x.sel])
	}
	return nil
}

// span is the page's range: its last days up to the end of today, or the custom one.
func (x *statsPlugin) span(q statsQuery) (time.Time, time.Time) {
	if statsRanges[q.rng].days == 0 && !q.from.IsZero() {
		return q.from, q.to
	}
	days := statsRanges[q.rng].days
	if days == 0 {
		days = 30
	}
	now := x.now().In(x.loc)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, x.loc).AddDate(0, 0, 1)
	return end.AddDate(0, 0, -days), end
}

func (x *statsPlugin) offset() int {
	_, off := x.now().In(x.loc).Zone()
	return off
}

// query is the page's query.
func (x *statsPlugin) query() stats.Query {
	from, to := x.span(x.q)
	return stats.Query{Group: x.q.group, From: from, To: to, Offset: x.offset(), Filters: cloneFilters(x.q.filters), Split: x.split()}
}

func queryKey(q stats.Query) string {
	b, _ := json.Marshal(q)
	return string(b)
}

// reportMeta is when an answer was read, and the load's state for the widgets.
type reportMeta struct {
	at    time.Time
	state widget.LoadState
}

// fetchState is a cached answer's load state: Pending while a fetch runs (Stale when there's an answer to show),
// Failed when the last fetch failed with nothing to show.
func fetchState(since time.Time, have bool, err error) widget.LoadState {
	switch {
	case !since.IsZero():
		return widget.LoadState{Phase: widget.Pending, Since: since, Stale: have, Err: err}
	case !have && err != nil:
		return widget.LoadState{Phase: widget.Failed, Err: err}
	case !have:
		return widget.LoadState{Phase: widget.Idle}
	}
	return widget.LoadState{Phase: widget.Ready, Err: err}
}

// report is q's answer as last read, asking again when it's old or missing.
func (x *statsPlugin) report(q stats.Query) (stats.Report, reportMeta) {
	key := queryKey(q)
	w := x.sh.cfg.Host
	x.mu.Lock()
	f, ok := x.reports[key]
	since := x.inflight[key]
	var meta reportMeta
	if ok && f.err == nil {
		meta.at = f.at
	}
	old := !ok || x.now().Sub(f.at) > refetchAfter || f.at.Before(x.changed) && x.now().Sub(f.at) > liveAfter
	x.mu.Unlock()
	if old && w != nil && since.IsZero() {
		since = x.fetch(key, func() (any, error) { return w.StatsQuery(q), nil }, func(v any, err error, at time.Time) {
			r, _ := v.(stats.Report)
			x.reports[key] = fetched[stats.Report]{v: r, at: at, err: err}
		})
	}
	meta.state = fetchState(since, ok && f.err == nil, f.err)
	if !ok || f.err != nil {
		return stats.Report{Points: q.Points()}, meta
	}
	return f.v, meta
}

// retryReport forgets q's answer, so the next frame asks again.
func (x *statsPlugin) retryReport(q stats.Query) {
	x.mu.Lock()
	delete(x.reports, queryKey(q))
	x.mu.Unlock()
	x.h.Redraw()
}

// fetch asks the session host in the background, and stores its answer on the app's loop. It returns when it
// started.
func (x *statsPlugin) fetch(key string, ask func() (any, error), store func(any, error, time.Time)) time.Time {
	since := x.now()
	x.mu.Lock()
	x.inflight[key] = since
	x.mu.Unlock()
	go func() {
		v, err := ask()
		x.h.Do(func() {
			at := x.now()
			x.mu.Lock()
			store(v, err, at)
			delete(x.inflight, key)
			x.mu.Unlock()
		})
		x.h.Redraw()
	}()
	return since
}

// detail is a session's stats once read.
func (x *statsPlugin) detail(id core.SessionID) (stats.Detail, bool) {
	d, s := x.detailLoad(id)
	return d, s.Phase == widget.Ready || s.Stale
}

// detailLoad is a session's stats, asked for again when old (an Oriel session's, or one outside Oriel), and its load.
func (x *statsPlugin) detailLoad(id core.SessionID) (stats.Detail, widget.LoadState) {
	key := x.detailKey(id)
	x.mu.Lock()
	f, ok := x.details[key]
	since := x.inflight[key]
	x.mu.Unlock()
	if w := x.sh.cfg.Host; (!ok || x.now().Sub(f.at) > refetchAfter) && since.IsZero() && w != nil {
		req := host.StatsSessionReq{Session: id, Split: x.split(), Offset: x.offset()}
		since = x.fetch(key, func() (any, error) { return w.SessionStats(req) }, func(v any, err error, at time.Time) {
			d, _ := v.(stats.Detail)
			if err == nil || !ok {
				x.details[key] = fetched[stats.Detail]{v: d, at: at, err: err}
			}
		})
	}
	return f.v, fetchState(since, ok && f.err == nil, f.err)
}

func (x *statsPlugin) detailKey(id core.SessionID) string { return fmt.Sprintf("%s|%v", id, x.split()) }

// retryDetail forgets a session's failed answer, so the next frame asks again.
func (x *statsPlugin) retryDetail(id core.SessionID) {
	x.mu.Lock()
	delete(x.details, x.detailKey(id))
	x.mu.Unlock()
	x.h.Redraw()
}

// usageHistory is the readings of an account's plan over the last week, oldest first.
func (x *statsPlugin) usageHistory(acct core.ProviderUsage) []core.ProviderUsage {
	w := x.sh.cfg.Host
	x.mu.Lock()
	var out []core.ProviderUsage
	old := x.history.at.IsZero() || x.now().Sub(x.history.at) > refetchAfter
	var last time.Time
	for _, u := range x.history.v {
		if u.Same(acct) {
			out = append(out, u)
			if u.Sampled.After(last) {
				last = u.Sampled
			}
		}
	}
	for _, u := range x.live {
		if u.Same(acct) && u.Sampled.After(last) {
			out = append(out, u)
		}
	}
	busy := !x.inflight["usage.history"].IsZero()
	x.mu.Unlock()
	if old && !busy && w != nil {
		since := x.now().Add(-8 * 24 * time.Hour)
		x.fetch("usage.history", func() (any, error) { return w.UsageHistory(host.UsageHistoryReq{Since: since}), nil },
			func(v any, err error, at time.Time) {
				h, _ := v.([]core.ProviderUsage)
				x.history = fetched[[]core.ProviderUsage]{v: h, at: at, err: err}
			})
	}
	slices.SortStableFunc(out, func(a, b core.ProviderUsage) int { return a.Sampled.Compare(b.Sampled) })
	return out
}

// refreshUsage reads every provider's windows again, off the loop.
func (x *statsPlugin) refreshUsage(registry.Ctx) error {
	if x.reading.State().Phase != widget.Pending {
		x.reading.Reload()
	}
	return nil
}

// figure is the cost of today or this week, for the sidebar and the start view.
func (x *statsPlugin) figure(span string) string {
	now := x.now().In(x.loc)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, x.loc).AddDate(0, 0, 1)
	from := end.AddDate(0, 0, -1)
	if span == "week" {
		from = end.AddDate(0, 0, -7)
	}
	r, meta := x.report(stats.Query{From: from, To: end, Offset: x.offset(), Split: true})
	if meta.at.IsZero() {
		return ""
	}
	if span == "week" {
		return money(r.Totals.Cost) + " this week"
	}
	return money(r.Totals.Cost) + " today"
}

// usageFigure is the session in front's provider's weekly window: "wk 71%".
func (x *statsPlugin) usageFigure() string {
	u, ok := x.frontUsage()
	if !ok {
		return ""
	}
	var parts []string
	for _, w := range u.Windows {
		if w.Length >= 24*time.Hour || strings.HasPrefix(strings.ToLower(w.Name), "week") {
			parts = append(parts, fmt.Sprintf("%s %d%%", strings.ToLower(usageShort(w)), pct(w.Used)))
			break
		}
	}
	return strings.Join(parts, " ")
}

// frontUsage is the plan of the account the session in front runs on (its provider's); else the first read.
func (x *statsPlugin) frontUsage() (core.ProviderUsage, bool) {
	st := x.h.State()
	us := st.Usage
	if len(us) == 0 {
		return core.ProviderUsage{}, false
	}
	if se, ok := st.Session(st.Focus); ok {
		s := se.Snapshot()
		acct := ""
		if n := len(s.Agent.Accounts); n > 0 {
			acct = s.Agent.Accounts[n-1].Account
		}
		for _, v := range us {
			if v.Provider == s.Agent.Provider && (acct == "" || v.Account == acct) {
				return v, true
			}
		}
	}
	return us[0], true
}

// accountLabel is how an account reads: the user's label for it (usage.accounts), else its email or organisation.
func (x *statsPlugin) accountLabel(account, name string) string {
	v, _, _ := x.h.Options().Get(AccountsOption, registry.At{})
	if m, ok := v.(map[string]string); ok && m[account] != "" {
		return m[account]
	}
	return cmpOr(name, cmpOr(account, "unknown account"))
}

// accountTitle is an account as the Usage page's switcher names it: its label and plan.
func (x *statsPlugin) accountTitle(u core.ProviderUsage) string {
	return x.accountLabel(u.Account, u.AccountName) + " · " + cmpOr(u.Plan, providerName(u.Provider))
}

// openSession opens a session's stats as a main tab: an Oriel session's, or one outside Oriel the Stats page found.
func (x *statsPlugin) openSession(ctx registry.Ctx, id core.SessionID) error {
	if id == "" {
		id = x.h.State().Focus
	}
	title := ""
	if se, ok := x.h.Session(id); ok {
		title = se.Name()
	} else {
		g, found := x.found(id)
		if !found {
			return fmt.Errorf("no session %q", id)
		}
		title = cmpOr(g.Label, string(id))
	}
	x.open, x.usageOpen = false, false
	return x.h.Run(ctx, "view.open", registry.Args{"id": StatsSessionView, "instance": string(id), "place": "tab", "title": "Stats · " + title})
}

// found is a session's row in any answer the Stats page has had grouped by session.
func (x *statsPlugin) found(id core.SessionID) (stats.Group, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, f := range x.reports {
		for _, g := range f.v.Groups {
			if g.Key == string(id) && g.Origin != "" {
				return g, true
			}
		}
	}
	return stats.Group{}, false
}

// csv is the page's table.
func (x *statsPlugin) csv() string {
	r, _ := x.report(x.query())
	var b strings.Builder
	b.WriteString(x.q.group + ",sessions,requests,tokens,cost,share\n")
	for _, g := range r.Groups {
		share := 0.0
		if r.Totals.Cost > 0 {
			share = g.Cost / r.Totals.Cost
		}
		fmt.Fprintf(&b, "%s,%d,%d,%d,%.2f,%.4f\n", csvField(g.Label), g.Sessions, g.Requests, g.Tokens, g.Cost, share)
	}
	return b.String()
}

func csvField(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// pagesSource is the omnibox's pages: every global sidebar entry, with its figure and key.
func pagesSource(h plugin.Host) registry.PickerSource {
	return registry.PickerSource{ID: "pages", Title: "Pages", Order: 15,
		Items: func(registry.Ctx, string) []registry.PickerItem {
			var out []registry.PickerItem
			for _, e := range registry.GlobalEntries(h) {
				if !h.Available(e.Command, e.Args) {
					continue
				}
				note := ""
				if e.Note != nil {
					registry.Call(func() error { note = e.Note(""); return nil })
				}
				out = append(out, registry.PickerItem{ID: e.Name, Label: e.Title, Detail: note, Icon: "▸", Key: boundKey(h, e.Command, e.Args),
					Data: e})
			}
			return out
		},
		Run: func(ctx registry.Ctx, it registry.PickerItem) error {
			e := it.Data.(registry.RailEntry)
			return h.Run(ctx, e.Command, e.Args)
		},
	}
}

// money is dollars as the pages show them: $412.80, $15.2k.
func money(v float64) string {
	if v >= 10_000 {
		return fmt.Sprintf("$%.1fk", v/1000)
	}
	return fmt.Sprintf("$%.2f", v)
}

func pct(f float64) int { return int(f*100 + .5) }

// count is a number as the tables show it: 9,840, 15.2k, 612M.
func count(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 10_000_000:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 100_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 10_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	s := fmt.Sprint(n)
	if n >= 1000 {
		s = s[:len(s)-3] + "," + s[len(s)-3:]
	}
	return s
}

// accountKey is an account's plan as the Usage page keys it.
func accountKey(u core.ProviderUsage) string { return u.Provider + "|" + u.Account }
