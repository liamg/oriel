package stats

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/internal/clock"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/watch"
)

// Stats and Usage: the session stats tab, the global Stats page and the Usage page, built on what
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

type Plugin struct {
	kit.Disposer
	h     plugin.Host
	Clock clock.Clock    // the system's if nil
	Loc   *time.Location // where days start; time.Local if nil

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
	inflight map[string]time.Time   // when each fetch running was asked for
	asked    map[string]time.Time   // when each read subscribed but not yet run was asked for
	frameNow time.Time              // now, as the last frame that asked for a read saw it: the time reads off the loop go by
	subs     kit.Subs               // the reads frames keep asking for, from the app's manager
	used     map[string]bool        // the reads asked for since the last frame
	forced   map[string]bool        // reads to ask again however young their answer: a retry, or agents moving on
	live     []core.ProviderUsage   // readings since the history was fetched, from UsageSampled
	reading  *plugin.Load[struct{}] // Refresh's read of every provider's windows
}

// liveAfter is how soon a report is asked for again once the agents it counts have moved on.
const liveAfter = 10 * time.Second

func (x *Plugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "stats"} }

func (x *Plugin) Activate(_ context.Context, h plugin.Host) error {
	x.Add(h.Contribute(registry.Busy{Name: "stats", Busy: x.Busy, Source: "builtin:stats"}))
	x.h = h
	if x.Loc == nil {
		x.Loc = time.Local
	}
	x.q = statsQuery{rng: 1, group: stats.ByRepo, filters: map[string][]string{}}
	x.hover = -1
	x.burnWin = map[string]int{}
	x.reports = map[string]fetched[stats.Report]{}
	x.details = map[string]fetched[stats.Detail]{}
	x.inflight, x.asked, x.used, x.forced = map[string]time.Time{}, map[string]time.Time{}, map[string]bool{}, map[string]bool{}
	x.reading = plugin.NewLoad(h, x.readUsage, plugin.LoadOpts{Timeout: 2 * time.Minute})
	x.Add(h.Option(registry.OptionSpec{Name: SplitOption, Type: registry.Bool, Default: true, Scopes: registry.ScopeUser,
		Help: "Split a multi-repo session's cost over its repos, each request charged to the repo its file tool calls worked in. Off, each repo is charged the whole session. The Stats page's toggle and the session tab's \"count whole session in each\" set it."}))
	x.Add(h.Option(registry.OptionSpec{Name: AccountsOption, Type: registry.Map, Default: map[string]string{}, Scopes: registry.ScopeUser,
		KeyLabel: "account", ValueLabel: "label",
		Help: "Names for the accounts plans belong to, by identity: \"Work\", \"Personal\". Usage and Stats show them in place of the account's email or organisation."}))
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	for _, k := range x.keymaps() {
		x.Add(h.Keymap(k))
	}
	for id, v := range x.views() {
		x.Add(h.View(id, v))
	}
	for _, c := range x.contributions() {
		x.Add(h.Contribute(c))
	}
	x.Add(h.Subscribe(core.Filter{}, x.seen))
	x.Add(h.Picker(pagesSource(h)))
	x.Add(h.Framed(x.framed))
	x.Add(registry.DisposeFunc(x.subs.Close))
	return nil
}

// Page is the Stats page, a plugin of its own over the stats' data (Stats), loaded the first time it's asked for.
type Page struct {
	kit.Disposer
	Stats *Plugin
}

// Manifest loads it on the first use of one of its commands, or its global key.
func (p *Page) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "stats.page", Lazy: kit.LazyOn(p.Stats.pageCommands(), p.Stats.pageKeymaps()[:1])}
}

func (p *Page) Activate(_ context.Context, h plugin.Host) error {
	x := p.Stats
	for _, c := range x.pageCommands() {
		p.Add(h.Command(c))
	}
	for _, k := range x.pageKeymaps() {
		p.Add(h.Keymap(k))
	}
	p.Add(h.View(kit.OverlayPrefix+"stats", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return &ui.Func{Key: "stats", Fn: x.page} })))
	return nil
}

// readUsage is Refresh's read of every provider's plan windows, off the loop.
func (x *Plugin) readUsage(ctx context.Context, _ func(string, float64)) (struct{}, error) {
	w, _ := kit.StatsCalls(x.h)
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
}

// seen keeps the plan readings as they come, and notes when an agent did something that costs.
func (x *Plugin) seen(ev core.Event) {
	switch ev := ev.(type) {
	case core.UsageSampled:
		x.mu.Lock()
		x.live = append(x.live, ev.Usage)
		if n := len(x.live); n > 500 {
			x.live = x.live[n-500:]
		}
		x.mu.Unlock()
	case core.AgentChanged, core.TranscriptAppended, core.ThreadUpdated, core.SessionOpened:
		x.fresher()
	}
}

func (x *Plugin) commands() []registry.Command {
	has := func(registry.Args) bool { return kit.HasSessions(x.h) }
	return []registry.Command{
		{ID: "stats.session", Title: "Open a session's stats as a main tab: {id}, the one in front by default, or one outside Oriel",
			Available: has, Run: func(ctx registry.Ctx, a registry.Args) error {
				return x.openSession(ctx, core.SessionID(a.String("id")))
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
	}
}

// pageCommands are the Stats page's.
func (x *Plugin) pageCommands() []registry.Command {
	return []registry.Command{
		{ID: "Stats", Title: "The Stats page; :Stats {repo=api 7d session group=model}", Run: func(ctx registry.Ctx, a registry.Args) error {
			return x.ex(ctx, a.String("args"))
		}},
		{ID: "stats.open", Title: "Open the Stats page: {group} by, a {range} (7d, 30d, 90d), filtered by {repo}, {provider}, {account}, {session}, {kind}, {model}, {origin} (oriel, outside)",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.openPage(a) }},
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
				r, err := widget.ParseDateRange(a.String("range"), x.Loc)
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
			kit.CopyText(x.h, x.csv())
			return nil
		}},
	}
}

func (x *Plugin) keymaps() []registry.Keymap {
	return []registry.Keymap{
		{Mode: "global", Keys: "<D-U>", Command: "usage.open", Desc: "Usage"},
		{Mode: UsageKeyMode, Keys: "r", Command: "usage.refresh", Desc: "Read the windows again"},
	}
}

// pageKeymaps are the Stats page's: its global key first.
func (x *Plugin) pageKeymaps() []registry.Keymap {
	out := []registry.Keymap{
		{Mode: "global", Keys: "<D-S>", Command: "stats.open", Desc: "Stats"},
		{Mode: StatsKeyMode, Keys: "g", Desc: "+group by"},
		{Mode: StatsKeyMode, Keys: "c", Command: "stats.custom", Desc: "Custom range"},
	}
	for _, g := range statsGroups {
		out = append(out, registry.Keymap{Mode: StatsKeyMode, Keys: "g" + g.short, Command: "stats.group", Args: registry.Args{"by": g.key},
			Desc: "Group by " + g.key})
	}
	for _, k := range []registry.Keymap{
		{Keys: "[", Command: "stats.range", Args: registry.Args{"step": -1}, Desc: "Shorter range"},
		{Keys: "]", Command: "stats.range", Args: registry.Args{"step": 1}, Desc: "Longer range"},
		{Keys: "j", Command: "stats.move", Args: registry.Args{"by": 1}}, {Keys: "<Down>", Command: "stats.move", Args: registry.Args{"by": 1}},
		{Keys: "k", Command: "stats.move", Args: registry.Args{"by": -1}}, {Keys: "<Up>", Command: "stats.move", Args: registry.Args{"by": -1}},
		{Keys: "<CR>", Command: "stats.drill", Desc: "Drill in"},
		{Keys: "<BS>", Command: "stats.back", Desc: "Back out"},
	} {
		k.Mode = StatsKeyMode
		out = append(out, k)
	}
	return out
}

func (x *Plugin) views() map[string]uireg.View {
	fn := func(key string, f func(c *ui.Ctx)) uireg.View {
		return uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return &ui.Func{Key: key, Fn: f} })
	}
	return map[string]uireg.View{
		StatsSessionView:            sessionStatsView{x},
		kit.OverlayPrefix + "usage": fn("usage", x.usagePage),
		kit.TitleRight + "40-usage": uireg.GlobalView{View: fn("usage", x.indicators)},
	}
}

// contributions are the service, the pages' places, rail and start entries, and the HUD's indicators.
func (x *Plugin) contributions() []registry.Contribution {
	panel := func(key string, f func(c *ui.Ctx)) func(registry.Ctx) (ui.Node, error) {
		return func(registry.Ctx) (ui.Node, error) { return &ui.Func{Key: key, Fn: f}, nil }
	}
	return []registry.Contribution{
		registry.Service{Name: kit.StatsService, Impl: x, Source: "stats"},
		kit.PagePlace("stats", "Stats", "stats.open", &x.open), // a place in the history before the page loads
		kit.PagePlace("usage", "Usage", "usage.open", &x.usageOpen),
		uireg.RailEntry{Name: "stats", Icon: "stats", Title: "Stats", Command: "stats.open", Scope: uireg.ScopeGlobalEntry, Order: 10,
			Note: func(string) string { return x.figure("today") }},
		uireg.RailEntry{Name: "usage", Icon: "usage", Title: "Usage", Command: "usage.open", Scope: uireg.ScopeGlobalEntry, Order: 20,
			Note: func(string) string { return x.usageFigure() }},
		uireg.StartEntry{Name: "stats", Icon: "stats", Title: "Stats", Desc: "What the agents cost and did", Command: "stats.open", Order: 60, Link: true,
			Note: func() string { return x.figure("week") }},
		uireg.StartEntry{Name: "usage", Icon: "usage", Title: "Usage", Desc: "Each provider's plan windows", Command: "usage.open", Order: 70, Link: true,
			Note: x.usageFigure},
		uireg.Indicator{Name: "usage", Slot: uireg.SlotHUD, Order: 10, Title: "Plan usage", Help: kit.HUDHelp + "#usage",
			Note: x.usageNote, Build: panel("usage", x.usagePanel)},
		uireg.Indicator{Name: "today", Slot: uireg.SlotHUD, Order: 40, Title: "Today", Help: kit.HUDHelp + "#today",
			Note: func() string { return "since 00:00" }, Build: panel("today", x.todayPanel)},
	}
}

// split is stats.split_sessions.
func (x *Plugin) split() bool {
	v, _, _ := x.h.Options().Get(SplitOption, registry.At{})
	on, ok := v.(bool)
	return on || !ok
}

func (x *Plugin) setSplit(ctx registry.Ctx, on bool) error {
	if x.h.Available("settings.set", nil) {
		return x.h.Run(ctx, "settings.set", registry.Args{"name": SplitOption, "value": on, "scope": "user"})
	}
	return x.h.Options().Set(SplitOption, registry.LayerUser, registry.At{}, on)
}

// ex runs :Stats with its words: "session" for the session in front's tab, else a page with key=value filters, a
// range (7d) and a group.
func (x *Plugin) ex(ctx registry.Ctx, args string) error {
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

func (x *Plugin) openPage(a registry.Args) error {
	q := statsQuery{rng: x.q.rng, group: cmp.Or(a.String("group"), stats.ByRepo), filters: map[string][]string{}}
	if r := strings.ToUpper(a.String("range")); r != "" {
		i := slices.IndexFunc(statsRanges, func(s statsRange) bool { return s.label == r })
		days, err := widget.ParseDateRange(r, x.Loc)
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
func (x *Plugin) custom(r widget.DateRange) {
	x.q.rng, x.q.from, x.q.to, x.sel = len(statsRanges)-1, r.From, r.To.AddDate(0, 0, 1), 0
}

func (x *Plugin) openUsage() error {
	x.usageOpen, x.open = true, false
	return nil
}

func (x *Plugin) regroup(by string) error {
	if !slices.ContainsFunc(statsGroups, func(g statsGroup) bool { return g.key == by }) {
		return fmt.Errorf("no grouping %q: repo, session, kind, model, provider, account, origin or day", by)
	}
	x.q.group, x.sel = by, 0
	return nil
}

// setOrigin keeps the page to one origin's sessions, or every one's with "".
func (x *Plugin) setOrigin(origin string) error {
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
func (x *Plugin) drill(g stats.Group) {
	x.back = append(x.back, x.q.clone())
	q := x.q.clone()
	if q.group == stats.ByDay {
		day, err := time.ParseInLocation("2006-01-02", g.Key, x.Loc)
		if err == nil {
			q.rng, q.from, q.to = len(statsRanges)-1, day, day.AddDate(0, 0, 1)
		}
	} else if !slices.Contains(q.filters[q.group], g.Key) {
		q.filters[q.group] = []string{g.Key}
	}
	q.group = drillOrder[q.group]
	x.q, x.sel, x.hover = q, 0, -1
}

func (x *Plugin) drillSelected() error {
	r, _ := x.report(x.query())
	if x.sel >= 0 && x.sel < len(r.Groups) {
		x.drill(r.Groups[x.sel])
	}
	return nil
}

// span is the page's range: its last days up to the end of today, or the custom one.
func (x *Plugin) span(q statsQuery) (time.Time, time.Time) {
	if statsRanges[q.rng].days == 0 && !q.from.IsZero() {
		return q.from, q.to
	}
	days := statsRanges[q.rng].days
	if days == 0 {
		days = 30
	}
	now := x.now().In(x.Loc)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, x.Loc).AddDate(0, 0, 1)
	return end.AddDate(0, 0, -days), end
}

func (x *Plugin) offset() int {
	_, off := x.now().In(x.Loc).Zone()
	return off
}

// query is the page's query.
func (x *Plugin) query() stats.Query {
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

// report is q's answer as last read, read through the app's manager while frames ask for it.
func (x *Plugin) report(q stats.Query) (stats.Report, reportMeta) {
	key := queryKey(q)
	var since time.Time
	if w, _ := kit.StatsCalls(x.h); w != nil {
		since = x.keep(key, "report", func() time.Time { return x.reports[key].at }, func() (any, error) { return w.StatsQuery(q), nil }, func(v any, err error, at time.Time) {
			r, _ := v.(stats.Report)
			x.reports[key] = fetched[stats.Report]{v: r, at: at, err: err}
		})
	}
	x.mu.Lock()
	f, ok := x.reports[key]
	x.mu.Unlock()
	var meta reportMeta
	if ok && f.err == nil {
		meta.at = f.at
	}
	meta.state = fetchState(since, ok && f.err == nil, f.err)
	if !ok || f.err != nil {
		return stats.Report{Points: q.Points()}, meta
	}
	return f.v, meta
}

// retryReport forgets q's answer and reads it again.
func (x *Plugin) retryReport(q stats.Query) {
	key := queryKey(q)
	x.mu.Lock()
	delete(x.reports, key)
	x.forced[key] = true
	x.mu.Unlock()
	x.subs.Refresh(key)
	x.h.Redraw()
}

// statsNeed is how fresh what the stats show is kept: a minute old at most, nothing behind another app.
var statsNeed = watch.Need{MaxAge: kit.RefetchAfter}

// keep has key read through the app's manager for as long as frames keep asking for it: at once, then every
// kit.RefetchAfter, and sooner once agents have moved on (seen). A read skips asking while the answer kept (cached,
// x.mu held) is younger than kit.RefetchAfter, unless a retry forced it. ask runs off the loop; store keeps its
// answer on the loop, x.mu held. It answers when the read running, or about to run, was asked for; zero with none.
func (x *Plugin) keep(key, what string, cached func() time.Time, ask func() (any, error), store func(any, error, time.Time)) time.Time {
	x.mu.Lock()
	x.used[key] = true
	x.frameNow = x.now()
	x.mu.Unlock()
	m := kit.ManagerFor(x.h, "")
	if m == nil {
		return time.Time{}
	}
	x.subs.Hold(key, m, statsNeed, func() kit.Closer {
		x.mu.Lock()
		if at := cached(); x.forced[key] || at.IsZero() || x.frameNow.Sub(at) >= kit.RefetchAfter {
			x.asked[key] = x.frameNow
		}
		x.mu.Unlock()
		src := kit.Every("stats:"+key, func(context.Context) error {
			x.mu.Lock()
			asked := x.asked[key]
			delete(x.asked, key)
			forced := x.forced[key]
			delete(x.forced, key)
			if at := cached(); !forced && !at.IsZero() && x.frameNow.Sub(at) < kit.RefetchAfter {
				x.mu.Unlock()
				return nil // the answer kept is young enough
			}
			x.inflight[key] = cmp.Or(asked, x.frameNow)
			x.mu.Unlock()
			v, err := ask()
			x.h.Do(func() {
				at := x.now()
				x.mu.Lock()
				store(v, err, at)
				delete(x.inflight, key)
				x.mu.Unlock()
			})
			x.h.Redraw()
			return err
		}, nil)
		return watch.Subscribe(m, src, statsNeed, "stats: "+what)
	})
	x.mu.Lock()
	defer x.mu.Unlock()
	if at, ok := x.inflight[key]; ok {
		return at
	}
	return x.asked[key]
}

// framed lets go of the reads no frame asked for since the last.
func (x *Plugin) framed() {
	x.mu.Lock()
	used := x.used
	x.used = map[string]bool{}
	x.mu.Unlock()
	x.subs.KeepOnly(used)
}

// fresher reads again what's older than liveAfter: the agents it counts have moved on.
func (x *Plugin) fresher() {
	x.mu.Lock()
	var stale []string
	for key, f := range x.reports {
		if x.now().Sub(f.at) > liveAfter && x.inflight[key].IsZero() {
			stale = append(stale, key)
			x.forced[key] = true
		}
	}
	x.mu.Unlock()
	for _, key := range stale {
		x.subs.Refresh(key)
	}
}

// Busy says whether a read it asked the session host for is still out, or about to be.
func (x *Plugin) Busy() bool {
	x.mu.Lock()
	n := len(x.inflight)
	x.mu.Unlock()
	return n > 0 || x.subs.Busy()
}

// Session is a session's stats, once they've been read (kit.Stats).
func (x *Plugin) Session(id core.SessionID) (stats.Detail, bool) { return x.detail(id) }

// detail is a session's stats once read.
func (x *Plugin) detail(id core.SessionID) (stats.Detail, bool) {
	d, s := x.detailLoad(id)
	return d, s.Phase == widget.Ready || s.Stale
}

// detailLoad is a session's stats (an Oriel session's, or one outside Oriel), read while frames ask, and its load.
func (x *Plugin) detailLoad(id core.SessionID) (stats.Detail, widget.LoadState) {
	key := x.detailKey(id)
	var since time.Time
	if w, _ := kit.StatsCalls(x.h); w != nil {
		req := host.StatsSessionReq{Session: id, Split: x.split(), Offset: x.offset()}
		since = x.keep(key, "session", func() time.Time { return x.details[key].at }, func() (any, error) { return w.SessionStats(req) }, func(v any, err error, at time.Time) {
			d, _ := v.(stats.Detail)
			if _, had := x.details[key]; err == nil || !had {
				x.details[key] = fetched[stats.Detail]{v: d, at: at, err: err}
			}
		})
	}
	x.mu.Lock()
	f, ok := x.details[key]
	x.mu.Unlock()
	return f.v, fetchState(since, ok && f.err == nil, f.err)
}

func (x *Plugin) detailKey(id core.SessionID) string { return fmt.Sprintf("%s|%v", id, x.split()) }

// retryDetail forgets a session's failed answer and reads it again.
func (x *Plugin) retryDetail(id core.SessionID) {
	key := x.detailKey(id)
	x.mu.Lock()
	delete(x.details, key)
	x.forced[key] = true
	x.mu.Unlock()
	x.subs.Refresh(key)
	x.h.Redraw()
}

// usageHistory is the readings of an account's plan over the last week, oldest first.
func (x *Plugin) usageHistory(acct core.ProviderUsage) []core.ProviderUsage {
	if w, _ := kit.StatsCalls(x.h); w != nil {
		x.keep("usage.history", "usage history", func() time.Time { return x.history.at }, func() (any, error) {
			x.mu.Lock()
			since := x.frameNow.Add(-8 * 24 * time.Hour)
			x.mu.Unlock()
			return w.UsageHistory(host.UsageHistoryReq{Since: since}), nil
		}, func(v any, err error, at time.Time) {
			h, _ := v.([]core.ProviderUsage)
			x.history = fetched[[]core.ProviderUsage]{v: h, at: at, err: err}
		})
	}
	x.mu.Lock()
	var out []core.ProviderUsage
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
	x.mu.Unlock()
	slices.SortStableFunc(out, func(a, b core.ProviderUsage) int { return a.Sampled.Compare(b.Sampled) })
	return out
}

// refreshUsage reads every provider's windows again, off the loop.
func (x *Plugin) refreshUsage(registry.Ctx) error {
	if x.reading.State().Phase != widget.Pending {
		x.reading.Reload()
	}
	return nil
}

// figure is the cost of today or this week, for the sidebar and the start view.
func (x *Plugin) figure(span string) string {
	now := x.now().In(x.Loc)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, x.Loc).AddDate(0, 0, 1)
	from := end.AddDate(0, 0, -1)
	if span == "week" {
		from = end.AddDate(0, 0, -7)
	}
	r, meta := x.report(stats.Query{From: from, To: end, Offset: x.offset(), Split: true})
	if meta.at.IsZero() {
		return ""
	}
	if span == "week" {
		return kit.Money(r.Totals.Cost) + " this week"
	}
	return kit.Money(r.Totals.Cost) + " today"
}

// usageFigure is the session in front's provider's weekly window: "wk 71%".
func (x *Plugin) usageFigure() string {
	u, ok := x.frontUsage()
	if !ok {
		return ""
	}
	var parts []string
	for _, w := range u.Windows {
		if w.Length >= 24*time.Hour || strings.HasPrefix(strings.ToLower(w.Name), "week") {
			parts = append(parts, fmt.Sprintf("%s %d%%", strings.ToLower(usageShort(w)), kit.Pct(w.Used)))
			break
		}
	}
	return strings.Join(parts, " ")
}

// frontUsage is the plan of the account the session in front runs on (its provider's); else the first read.
func (x *Plugin) frontUsage() (core.ProviderUsage, bool) {
	st := x.h.State()
	us := st.Usage
	if len(us) == 0 {
		return core.ProviderUsage{}, false
	}
	if u, ok := x.sessionUsage(us); ok {
		return u, true
	}
	used := usedProviders(st, x.now())
	if i := slices.IndexFunc(us, func(u core.ProviderUsage) bool { return used[u.Provider] }); i >= 0 {
		return us[i], true
	}
	if len(used) > 0 {
		return core.ProviderUsage{}, false // the plans in use weren't read: not another's in their place
	}
	return us[0], true
}

// accountLabel is how an account reads: the user's label for it (usage.accounts), else its email or organisation.
func (x *Plugin) accountLabel(account, name string) string {
	v, _, _ := x.h.Options().Get(AccountsOption, registry.At{})
	if m, ok := v.(map[string]string); ok && m[account] != "" {
		return m[account]
	}
	return cmp.Or(name, cmp.Or(account, "unknown account"))
}

// accountTitle is an account as the Usage page's switcher names it: its label and plan.
func (x *Plugin) accountTitle(u core.ProviderUsage) string {
	return x.accountLabel(u.Account, u.AccountName) + " · " + cmp.Or(u.Plan, services.ProviderTitle(x.h, u.Provider))
}

// openSession opens a session's stats as a main tab: an Oriel session's, or one outside Oriel the Stats page found.
func (x *Plugin) openSession(ctx registry.Ctx, id core.SessionID) error {
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
		title = cmp.Or(g.Label, string(id))
	}
	x.open, x.usageOpen = false, false
	return x.h.Run(ctx, "view.open", registry.Args{"id": StatsSessionView, "instance": string(id), "place": "tab", "title": "Stats · " + title})
}

// found is a session's row in any answer the Stats page has had grouped by session.
func (x *Plugin) found(id core.SessionID) (stats.Group, bool) {
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
func (x *Plugin) csv() string {
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
func pagesSource(h plugin.Host) uireg.PickerSource {
	return uireg.PickerSource{ID: "pages", Title: "Pages", Order: 15,
		Items: func(registry.Ctx, string) []uireg.PickerItem {
			var out []uireg.PickerItem
			for _, e := range uireg.GlobalEntries(h) {
				if !h.Available(e.Command, e.Args) {
					continue
				}
				note := ""
				if e.Note != nil {
					registry.Call(func() error { note = e.Note(""); return nil })
				}
				out = append(out, uireg.PickerItem{ID: e.Name, Label: e.Title, Detail: note, Icon: "▸", Key: kit.BoundKey(h, e.Command, e.Args),
					Data: e})
			}
			return out
		},
		Run: func(ctx registry.Ctx, it uireg.PickerItem) error {
			e := it.Data.(uireg.RailEntry)
			return h.Run(ctx, e.Command, e.Args)
		},
	}
}

// accountKey is an account's plan as the Usage page keys it.
func accountKey(u core.ProviderUsage) string { return u.Provider + "|" + u.Account }

func (x *Plugin) now() time.Time { return clock.Or(x.Clock).Now() }
