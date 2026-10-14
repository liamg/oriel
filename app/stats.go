package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/worker"
	"github.com/liamg/oriel/worker/stats"
)

// Stats and Usage (AI-12, mockup 43): the session stats tab, the global Stats page and the Usage page, built on what
// every worker answers (stats.query, stats.session, usage.history), and the ways to them: the sidebar's global
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
	{stats.ByProvider, "PROVIDER", "p"}, {stats.ByAccount, "ACCOUNT", "a"}, {stats.ByDay, "DAY", "d"},
}

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

// fetched is a worker's last answer.
type fetched[T any] struct {
	v   T
	at  time.Time
	err error
}

type statsExt struct {
	disposer
	sh  *shell
	h   ext.Host
	now func() time.Time
	loc *time.Location

	open  bool       // the Stats page
	q     statsQuery // what it shows
	back  []statsQuery
	sel   int // the table's selected row
	hover int // the row or series under the pointer last frame, -1
	// hovering is the one under it this frame.
	hovering int

	usageOpen bool
	acct      string         // the Usage page's account, provider and identity (accountKey); "" for the session in front's
	burnWin   map[string]int // the Usage page's window picked for "burned the window", by account

	mu       sync.Mutex
	reports  map[string]map[core.WorkerID]fetched[stats.Report] // by query
	details  map[string]fetched[stats.Detail]                   // by session and split
	history  map[core.WorkerID]fetched[[]core.ProviderUsage]
	inflight map[string]bool
}

func (x *statsExt) Manifest() ext.Manifest { return ext.Manifest{ID: "stats"} }

func (x *statsExt) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	if x.now == nil {
		x.now = time.Now
	}
	if x.loc == nil {
		x.loc = time.Local
	}
	x.q = statsQuery{rng: 1, group: stats.ByRepo, filters: map[string][]string{}}
	x.hover = -1
	x.burnWin = map[string]int{}
	x.reports = map[string]map[core.WorkerID]fetched[stats.Report]{}
	x.details = map[string]fetched[stats.Detail]{}
	x.history = map[core.WorkerID]fetched[[]core.ProviderUsage]{}
	x.inflight = map[string]bool{}
	x.add(h.Option(registry.OptionSpec{Name: SplitOption, Type: registry.Bool, Default: true, Scopes: registry.ScopeUser,
		Help: "Split a multi-repo session's cost over its repos by each one's share of edits and file tool calls. Off, each repo is charged the whole session. The Stats page's toggle and the session tab's \"count whole session in each\" set it."}))
	x.add(h.Option(registry.OptionSpec{Name: AccountsOption, Type: registry.Map, Default: map[string]string{}, Scopes: registry.ScopeUser,
		KeyLabel: "account", ValueLabel: "label",
		Help: "Names for the accounts plans belong to, by identity: \"Work\", \"Personal\". Usage and Stats show them in place of the account's email or organisation."}))
	workers := func(registry.Args) bool { return x.sh.sessions != nil && len(x.sh.sessions.clients()) > 0 }
	for _, c := range []registry.Command{
		{ID: "Stats", Title: "The Stats page; :Stats {repo=api 7d session group=model}", Run: func(ctx registry.Ctx, a registry.Args) error {
			return x.ex(ctx, a.String("args"))
		}},
		{ID: "stats.open", Title: "Open the Stats page: {group} by, a {range} (7d, 30d, 90d), filtered by {repo}, {worker}, {provider}, {account}, {session}, {kind}, {model}",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.openPage(a) }},
		{ID: "stats.session", Title: "Open a session's stats as a main tab: {id}, the one in front by default", Available: workers,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				return x.openSession(ctx, core.SessionID(a.String("id")))
			}},
		{ID: "stats.group", Title: "Group the Stats page {by} repo, session, kind, model, provider or day", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.regroup(a.String("by"))
		}},
		{ID: "stats.range", Title: "Step the Stats page's range {step} -1 shorter or 1 longer", Run: func(_ registry.Ctx, a registry.Args) error {
			x.q.rng = min(max(x.q.rng+a.Int("step"), 0), len(statsRanges)-2)
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
		{ID: "usage.refresh", Title: "Read every provider's plan windows again", Available: workers, Run: func(ctx registry.Ctx, _ registry.Args) error {
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
		{Name: "jobs", Icon: "jobs", Title: "Jobs", Command: "jobs.open", Scope: registry.ScopeGlobalEntry, Order: 30},
	} {
		x.add(h.Contribute(e))
	}
	for _, e := range []registry.StartEntry{
		{Name: "stats", Icon: "stats", Title: "Stats", Desc: "What the agents cost and did", Command: "stats.open", Order: 60, Link: true,
			Note: func() string { return x.figure("week") }},
		{Name: "usage", Icon: "usage", Title: "Usage", Desc: "Each provider's plan windows", Command: "usage.open", Order: 70, Link: true,
			Note: x.usageFigure},
		{Name: "jobs", Icon: "jobs", Title: "Jobs", Desc: "Saved prompts, their runs and schedules", Command: "jobs.open", Order: 80, Link: true},
	} {
		x.add(h.Contribute(e))
	}
	x.add(h.Picker(pagesSource(h)))
	x.add(h.View(StatsSessionView, sessionStatsView{x}))
	x.add(h.View(OverlayPrefix+"stats", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "stats", Fn: x.page} })))
	x.add(h.View(OverlayPrefix+"usage", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "usage", Fn: x.usagePage} })))
	x.add(h.View(TitleRight+"40-usage", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "usage", Fn: x.indicators} })))
	return nil
}

// split is stats.split_sessions.
func (x *statsExt) split() bool {
	v, _, _ := x.h.Options().Get(SplitOption, registry.At{})
	on, ok := v.(bool)
	return on || !ok
}

func (x *statsExt) setSplit(ctx registry.Ctx, on bool) error {
	if x.h.Available("settings.set", nil) {
		return x.h.Run(ctx, "settings.set", registry.Args{"name": SplitOption, "value": on, "scope": "user"})
	}
	return x.h.Options().Set(SplitOption, registry.LayerUser, registry.At{}, on)
}

// ex runs :Stats with its words: "session" for the session in front's tab, else a page with key=value filters, a
// range (7d) and a group.
func (x *statsExt) ex(ctx registry.Ctx, args string) error {
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

func (x *statsExt) openPage(a registry.Args) error {
	q := statsQuery{rng: x.q.rng, group: cmpOr(a.String("group"), stats.ByRepo), filters: map[string][]string{}}
	if r := strings.ToUpper(a.String("range")); r != "" {
		i := slices.IndexFunc(statsRanges, func(s statsRange) bool { return s.label == r })
		if i < 0 {
			return fmt.Errorf("a range is 7d, 30d or 90d, not %q", a.String("range"))
		}
		q.rng = i
	}
	if !slices.ContainsFunc(statsGroups, func(g statsGroup) bool { return g.key == q.group }) {
		return fmt.Errorf("no grouping %q: repo, session, kind, model, provider, account or day", q.group)
	}
	for _, k := range []string{"repo", "worker", "provider", "account", "session", "kind", "model"} {
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

func (x *statsExt) openUsage() error {
	x.usageOpen, x.open = true, false
	return nil
}

func (x *statsExt) regroup(by string) error {
	if !slices.ContainsFunc(statsGroups, func(g statsGroup) bool { return g.key == by }) {
		return fmt.Errorf("no grouping %q: repo, session, kind, model, provider, account or day", by)
	}
	x.q.group, x.sel = by, 0
	return nil
}

// drillOrder is what a group's rows regroup by when drilled into.
var drillOrder = map[string]string{stats.ByRepo: stats.BySession, stats.BySession: stats.ByModel, stats.ByKind: stats.BySession,
	stats.ByModel: stats.BySession, stats.ByProvider: stats.ByModel, stats.ByAccount: stats.BySession, stats.ByDay: stats.BySession}

// drill narrows the page to a group's row: its filter chip, regrouped. A day becomes the range.
func (x *statsExt) drill(g stats.Group) {
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

func (x *statsExt) drillSelected() error {
	r, _ := x.report(x.query())
	if x.sel >= 0 && x.sel < len(r.Groups) {
		x.drill(r.Groups[x.sel])
	}
	return nil
}

// span is the page's range: its last days up to the end of today, or the custom one.
func (x *statsExt) span(q statsQuery) (time.Time, time.Time) {
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

func (x *statsExt) offset() int {
	_, off := x.now().In(x.loc).Zone()
	return off
}

// query is the page's query.
func (x *statsExt) query() stats.Query {
	from, to := x.span(x.q)
	return stats.Query{Group: x.q.group, From: from, To: to, Offset: x.offset(), Filters: cloneFilters(x.q.filters), Split: x.split()}
}

func queryKey(q stats.Query) string {
	b, _ := json.Marshal(q)
	return string(b)
}

// reportMeta is where a merged answer came from.
type reportMeta struct {
	workers []core.WorkerID
	stale   []core.WorkerID // offline: their last answer
	at      time.Time       // the oldest answer's
	loading bool
}

func (x *statsExt) clients() map[core.WorkerID]worker.Client {
	if x.sh.sessions == nil {
		return nil
	}
	return x.sh.sessions.byWorker()
}

// report is q's answer: every worker's last, merged, asking again when it's old or missing.
func (x *statsExt) report(q stats.Query) (stats.Report, reportMeta) {
	key := queryKey(q)
	clients := x.clients()
	x.mu.Lock()
	got := x.reports[key]
	var rs []stats.Report
	var meta reportMeta
	old := len(got) == 0
	for id, f := range got {
		if f.err != nil {
			continue
		}
		rs = append(rs, f.v)
		meta.workers = append(meta.workers, id)
		if _, live := clients[id]; !live {
			meta.stale = append(meta.stale, id)
		}
		if meta.at.IsZero() || f.at.Before(meta.at) {
			meta.at = f.at
		}
		if x.now().Sub(f.at) > refetchAfter {
			old = true
		}
	}
	for id := range clients {
		if _, ok := got[id]; !ok {
			old = true
		}
	}
	meta.loading = x.inflight[key]
	x.mu.Unlock()
	slices.Sort(meta.workers)
	if old && len(clients) > 0 && !meta.loading {
		x.fetch(key, clients, func(ctx context.Context, c worker.Client) (any, error) { return c.StatsQuery(ctx, q) },
			func(id core.WorkerID, v any, err error, at time.Time) {
				if x.reports[key] == nil {
					x.reports[key] = map[core.WorkerID]fetched[stats.Report]{}
				}
				r, _ := v.(stats.Report)
				x.reports[key][id] = fetched[stats.Report]{v: r, at: at, err: err}
			})
		meta.loading = true
	}
	if len(rs) == 0 {
		return stats.Report{Points: q.Points()}, meta
	}
	return stats.Merge(rs...), meta
}

// fetch asks every worker in the background, and stores each answer on the app's loop.
func (x *statsExt) fetch(key string, clients map[core.WorkerID]worker.Client, ask func(context.Context, worker.Client) (any, error),
	store func(core.WorkerID, any, error, time.Time)) {
	x.mu.Lock()
	x.inflight[key] = true
	x.mu.Unlock()
	go func() {
		type answer struct {
			id  core.WorkerID
			v   any
			err error
		}
		var out []answer
		for id, c := range clients {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			v, err := ask(ctx, c)
			cancel()
			out = append(out, answer{id, v, err})
		}
		x.h.Do(func() {
			at := x.now()
			x.mu.Lock()
			for _, a := range out {
				store(a.id, a.v, a.err, at)
			}
			delete(x.inflight, key)
			x.mu.Unlock()
		})
		x.h.Redraw()
	}()
}

// detail is a session's stats from its worker, asked for again when old.
func (x *statsExt) detail(id core.SessionID) (stats.Detail, bool) {
	split := x.split()
	key := fmt.Sprintf("%s|%v", id, split)
	x.mu.Lock()
	f, ok := x.details[key]
	busy := x.inflight[key]
	x.mu.Unlock()
	if (!ok || x.now().Sub(f.at) > refetchAfter) && !busy && x.sh.sessions != nil {
		if c, err := x.sh.sessions.client(id); err == nil {
			wid := core.LocalWorker
			if se, ok := x.h.Session(id); ok && se.Snapshot().Worker != "" {
				wid = se.Snapshot().Worker
			}
			req := worker.StatsSessionReq{Session: id, Split: split, Offset: x.offset()}
			x.fetch(key, map[core.WorkerID]worker.Client{wid: c}, func(ctx context.Context, c worker.Client) (any, error) {
				return c.SessionStats(ctx, req)
			}, func(_ core.WorkerID, v any, err error, at time.Time) {
				d, _ := v.(stats.Detail)
				if err == nil || !ok {
					x.details[key] = fetched[stats.Detail]{v: d, at: at, err: err}
				}
			})
		}
	}
	return f.v, ok && f.err == nil
}

// usageHistory is every worker's readings of an account's plan over the last week, oldest first: two workers on
// one account read the same plan.
func (x *statsExt) usageHistory(acct core.ProviderUsage) []core.ProviderUsage {
	clients := x.clients()
	x.mu.Lock()
	var out []core.ProviderUsage
	old := false
	for id := range clients {
		f, ok := x.history[id]
		if !ok || x.now().Sub(f.at) > refetchAfter {
			old = true
		}
	}
	for _, f := range x.history {
		for _, u := range f.v {
			if u.Same(acct) {
				out = append(out, u)
			}
		}
	}
	busy := x.inflight["usage.history"]
	x.mu.Unlock()
	if old && !busy && len(clients) > 0 {
		since := x.now().Add(-8 * 24 * time.Hour)
		x.fetch("usage.history", clients, func(ctx context.Context, c worker.Client) (any, error) {
			return c.UsageHistory(ctx, worker.UsageHistoryReq{Since: since})
		}, func(id core.WorkerID, v any, err error, at time.Time) {
			h, _ := v.([]core.ProviderUsage)
			x.history[id] = fetched[[]core.ProviderUsage]{v: h, at: at, err: err}
		})
	}
	slices.SortStableFunc(out, func(a, b core.ProviderUsage) int { return a.Sampled.Compare(b.Sampled) })
	return out
}

func (x *statsExt) refreshUsage(ctx registry.Ctx) error {
	for _, c := range x.clients() {
		if err := c.ReadUsage(ctx); err != nil {
			x.h.Log().Warn("usage", "err", err)
		}
	}
	x.mu.Lock()
	clear(x.history)
	x.mu.Unlock()
	x.h.Redraw()
	return nil
}

// figure is the cost of today or this week, for the sidebar and the start view.
func (x *statsExt) figure(span string) string {
	now := x.now().In(x.loc)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, x.loc).AddDate(0, 0, 1)
	from := end.AddDate(0, 0, -1)
	if span == "week" {
		from = end.AddDate(0, 0, -7)
	}
	r, meta := x.report(stats.Query{From: from, To: end, Offset: x.offset(), Split: true})
	if len(meta.workers) == 0 {
		return ""
	}
	if span == "week" {
		return money(r.Totals.Cost) + " this week"
	}
	return money(r.Totals.Cost) + " today"
}

// usageFigure is the session in front's provider's weekly window: "wk 71%".
func (x *statsExt) usageFigure() string {
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

// frontUsage is the plan of the account the session in front runs on (its provider's, on its worker); with no
// session, the local worker's; else the first read.
func (x *statsExt) frontUsage() (core.ProviderUsage, bool) {
	st := x.h.State()
	us := st.Usage
	if len(us) == 0 {
		return core.ProviderUsage{}, false
	}
	if se, ok := st.Session(st.Focus); ok {
		s := se.Snapshot()
		wid := cmpOr(string(s.Worker), string(core.LocalWorker))
		for _, v := range us {
			if v.Provider == s.Agent.Provider && v.Uses(core.WorkerID(wid)) {
				return v, true
			}
		}
	}
	for _, v := range us {
		if v.Uses(core.LocalWorker) {
			return v, true
		}
	}
	return us[0], true
}

// accountLabel is how an account reads: the user's label for it (usage.accounts), else its email or organisation.
func (x *statsExt) accountLabel(account, name string) string {
	v, _, _ := x.h.Options().Get(AccountsOption, registry.At{})
	if m, ok := v.(map[string]string); ok && m[account] != "" {
		return m[account]
	}
	return cmpOr(name, cmpOr(account, "unknown account"))
}

// accountTitle is an account as the Usage page's switcher names it: its label, plan and workers.
func (x *statsExt) accountTitle(u core.ProviderUsage) string {
	parts := []string{x.accountLabel(u.Account, u.AccountName), cmpOr(u.Plan, providerName(u.Provider))}
	var ws []string
	for _, w := range u.Workers {
		ws = append(ws, x.workerName(w))
	}
	slices.Sort(ws)
	if len(ws) > 0 {
		parts = append(parts, strings.Join(ws, ", "))
	}
	return strings.Join(parts, " · ")
}

// openSession opens a session's stats as a main tab.
func (x *statsExt) openSession(ctx registry.Ctx, id core.SessionID) error {
	if id == "" {
		id = x.h.State().Focus
	}
	se, ok := x.h.Session(id)
	if !ok {
		return fmt.Errorf("no session %q", id)
	}
	x.open, x.usageOpen = false, false
	return x.h.Run(ctx, "view.open", registry.Args{"id": StatsSessionView, "instance": string(id), "place": "tab", "title": "Stats · " + se.Name()})
}

// csv is the page's table.
func (x *statsExt) csv() string {
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
func pagesSource(h ext.Host) registry.PickerSource {
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
