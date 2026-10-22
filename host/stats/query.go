package stats

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/agent/price"
)

// Session is a session as a query sees it: what it is, the repos it works in, and its logs' scans.
type Session struct {
	ID       string
	Name     string
	Kind     string
	Provider string
	Model    string
	Effort   string
	// Accounts are the provider's accounts the session ran on (their plans), oldest first: each from when it was
	// recorded until the next.
	Accounts []Account
	Live     bool // its agent is running
	Repos    []Repo
	Scans    []*Scan
	Origin   string // OriginOriel or OriginOutside
}

// Origins: a session Oriel ran, or one found in a provider's logs that no Oriel session owns.
const (
	OriginOriel   = "oriel"
	OriginOutside = "outside"
)

// originLabel is how an origin reads.
func originLabel(o string) string {
	if o == OriginOutside {
		return "outside Oriel"
	}
	return "Oriel"
}

// originOf is a session's origin: Oriel's unless it says otherwise.
func originOf(s Session) string { return cmpOr(s.Origin, OriginOriel) }

// Account is a provider's account a session ran on from At: its identity, and how it reads.
type Account struct {
	At   time.Time
	ID   string
	Name string
}

// accountAt is the account the session ran on in hour (unix hours): the latest recorded by its end, else the first.
func (s Session) accountAt(hour int64) Account {
	if len(s.Accounts) == 0 {
		return Account{}
	}
	a := s.Accounts[0]
	end := (hour + 1) * 3600
	for _, x := range s.Accounts[1:] {
		if x.At.Unix() < end {
			a = x
		}
	}
	return a
}

// Repo is one of a session's projects: its name, and the directory it's checked out in for the session.
type Repo struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

// Share is a repo's part of a session: its share, the file tool calls that fell in it, and its cost.
type Share struct {
	Repo  string  `json:"repo"`
	Share float64 `json:"share"`
	Files int64   `json:"files"`
	Cost  float64 `json:"cost"`
}

// Shares are each repo's share of the session by its edits and file tool calls (FileTools): how what wasn't worked in
// any repo is split. A session with no file work splits evenly; one with no repo has none.
func (s Session) Shares() []Share {
	if len(s.Repos) == 0 {
		return nil
	}
	out := make([]Share, len(s.Repos))
	var total int64
	for i, r := range s.Repos {
		out[i].Repo = r.Name
	}
	for _, sc := range s.Scans {
		for p, n := range sc.Paths {
			if i := s.repoOf(p); i >= 0 {
				out[i].Files += n
				total += n
			}
		}
	}
	for i := range out {
		if total == 0 {
			out[i].Share = 1 / float64(len(out))
		} else {
			out[i].Share = float64(out[i].Files) / float64(total)
		}
	}
	return out
}

// repoOf is the index of the repo path is in, the innermost when they nest, or -1.
func (s Session) repoOf(path string) int {
	best := -1
	if path == "" {
		return best
	}
	for i, r := range s.Repos {
		if r.Dir != "" && within(path, r.Dir) && (best < 0 || len(r.Dir) > len(s.Repos[best].Dir)) {
			best = i
		}
	}
	return best
}

// charged are the repos slot sl is charged to: the one it was worked in, else the session's shares. Unsplit, each repo
// is charged all of it.
func (s Session) charged(sl *Slot, shares []Share, split bool) []Share {
	if !split {
		out := make([]Share, len(shares))
		for i, sh := range shares {
			out[i] = Share{Repo: sh.Repo, Share: 1}
		}
		return out
	}
	if i := s.repoOf(sl.Place); i >= 0 {
		return []Share{{Repo: s.Repos[i].Name, Share: 1}}
	}
	return shares
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

// Group keys.
const (
	ByRepo     = "repo"
	BySession  = "session"
	ByKind     = "kind"
	ByModel    = "model"
	ByProvider = "provider"
	ByAccount  = "account"
	ByDay      = "day"
	ByOrigin   = "origin"
)

// Query asks for the stats of a range, grouped one way and filtered.
type Query struct {
	Group string    `json:"group,omitempty"` // ByRepo when ""
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
	Step  string    `json:"step,omitempty"` // "day" (the default) or "hour": the series' resolution
	// Offset is the zone the days and hours of the day are in, in seconds east of UTC.
	Offset int `json:"offset,omitempty"`
	// Filters keep what matches any of a key's values: repo, session, kind, model, provider, account, origin.
	Filters map[string][]string `json:"filters,omitempty"`
	// Split charges each of a multi-repo session's requests to the repo it worked in (Session.charged); otherwise each
	// repo is charged the whole session.
	Split bool `json:"split,omitempty"`
}

// Totals are a range's headline numbers.
type Totals struct {
	Sessions   int   `json:"sessions"`
	MultiRepo  int   `json:"multiRepo,omitempty"` // sessions with more than one repo
	Pairs      int   `json:"pairs,omitempty"`     // repo-sessions: each session once for each of its repos
	Requests   int64 `json:"requests"`
	Turns      int64 `json:"turns"`
	ToolCalls  int64 `json:"toolCalls"`
	ToolErrors int64 `json:"toolErrors"`
	ActiveS    int64 `json:"activeS"`
	// SpanS is the wall-clock of the sessions: on each day each one worked, its first record to its last, added up.
	SpanS  int64   `json:"spanS,omitempty"`
	Tokens Tokens  `json:"tokens"`
	Cost   float64 `json:"cost"`
}

// Group is one row of a grouping, with its series.
type Group struct {
	Key      string    `json:"key"`
	Label    string    `json:"label"`
	Repos    []string  `json:"repos,omitempty"` // a session's
	Sessions int       `json:"sessions"`
	Requests int64     `json:"requests"`
	Tokens   int64     `json:"tokens"`
	Cost     float64   `json:"cost"`
	Last     time.Time `json:"last,omitzero"`
	Origin   string    `json:"origin,omitempty"` // a session's
	Series   []float64 `json:"series"`           // cost at each of the report's points
	Used     []int64   `json:"used"`             // tokens at each point
	sessions map[string]bool
}

// Origin is an origin's part of a range: Oriel's sessions, or those outside it.
type Origin struct {
	Name     string  `json:"name"`
	Sessions int     `json:"sessions"`
	Requests int64   `json:"requests"`
	Tokens   Tokens  `json:"tokens"`
	Cost     float64 `json:"cost"`
}

// Model is a model's part of a range.
type Model struct {
	Name      string  `json:"name"`
	Requests  int64   `json:"requests"`
	Sidechain int64   `json:"sidechain,omitempty"` // requests from subagents
	Tokens    int64   `json:"tokens"`
	Cost      float64 `json:"cost"`
}

// ToolRow is a tool's calls, errors and timings.
type ToolRow struct {
	Name    string         `json:"name"`
	Calls   int64          `json:"calls"`
	Errors  int64          `json:"errors"`
	Buckets [Buckets]int64 `json:"buckets"`
	P50     int64          `json:"p50,omitempty"` // ms
}

// Effort is a reasoning effort's part of the requests.
type Effort struct {
	Name     string  `json:"name"`
	Requests int64   `json:"requests"`
	Cost     float64 `json:"cost"`
}

// Report is what a query answers: what both stats pages are drawn from.
type Report struct {
	Scanned time.Time   `json:"scanned,omitzero"`
	Points  []time.Time `json:"points"`
	Totals  Totals      `json:"totals"`
	Prev    Totals      `json:"prev"` // the period of the same length before
	Groups  []Group     `json:"groups"`
	Tokens  Tokens      `json:"tokens"` // by kind
	Costs   Costs       `json:"costs"`
	Models  []Model     `json:"models"`
	Tools   []ToolRow   `json:"tools"`
	Hours   [24]float64 `json:"hours"` // cost by hour of the day
	Efforts []Effort    `json:"efforts"`
	Origins []Origin    `json:"origins,omitempty"` // Oriel's, then outside's: those with any requests
	Prices  Prices      `json:"prices,omitzero"`
}

// Prices are where the costs' prices come from.
type Prices struct {
	Source  string    `json:"source,omitempty"` // "cost.dev"
	Updated time.Time `json:"updated,omitzero"` // when the source last published the list
}

// PricesOf says where b's prices come from.
func PricesOf(b *price.Book) Prices {
	if b == nil {
		return Prices{}
	}
	return Prices{Source: price.Source, Updated: b.Generated()}
}

// step is the query's resolution in seconds.
func (q Query) step() int64 {
	if q.Step == "hour" {
		return 3600
	}
	return 86400
}

// Points are the starts of the steps in [From, To), in the query's zone.
func (q Query) Points() []time.Time {
	st := q.step()
	zone := time.FixedZone("", q.Offset)
	var out []time.Time
	for i := q.index(q.From.Unix()); ; i++ {
		at := time.Unix(i*st-int64(q.Offset), 0).In(zone)
		if !at.Before(q.To) {
			break
		}
		out = append(out, at)
	}
	return out
}

func (q Query) index(unix int64) int64 {
	st := q.step()
	v := unix + int64(q.Offset)
	if v < 0 {
		return (v - st + 1) / st
	}
	return v / st
}

func (q Query) has(key string, vals ...string) bool {
	want, ok := q.Filters[key]
	if !ok || len(want) == 0 {
		return true
	}
	for _, v := range vals {
		if slices.Contains(want, v) {
			return true
		}
	}
	return false
}

// weight is how much of session s a query counts: 0 when a filter leaves it out; with a repo filter, the share of
// the repos it keeps (or all of it, unsplit). The account filter is the hour's (keeps), and a slot's own repo its
// (slotWeight).
func (q Query) weight(s Session, shares []Share) float64 {
	if !q.has("session", s.ID) || !q.has("kind", s.Kind) || !q.has("provider", s.Provider) ||
		!q.has("origin", originOf(s)) {
		return 0
	}
	if len(q.Filters["repo"]) == 0 {
		return 1
	}
	w := 0.0
	for _, sh := range shares {
		if q.has("repo", sh.Repo) {
			if !q.Split {
				return 1
			}
			w += sh.Share
		}
	}
	return w
}

// slotWeight is how much of slot sl a query counts, w being its session's: with a repo filter and split, the part
// charged to the repos it keeps.
func (q Query) slotWeight(s Session, sl *Slot, shares []Share, w float64) float64 {
	if len(q.Filters["repo"]) == 0 || !q.Split {
		return w
	}
	x := 0.0
	for _, sh := range s.charged(sl, shares, true) {
		if q.has("repo", sh.Repo) {
			x += sh.Share
		}
	}
	return x
}

type part struct {
	key, label string
	repos      []string
	origin     string
	w          float64
}

// Run answers q from sessions.
func Run(q Query, sessions []Session) Report {
	if q.Group == "" {
		q.Group = ByRepo
	}
	t := newTally(q)
	for _, s := range sessions {
		t.session(s)
	}
	return t.report()
}

// tally is a Report as Run builds it, a session at a time.
type tally struct {
	q       Query
	r       Report
	groups  map[string]*Group
	models  map[string]*Model
	efforts map[string]*Effort
	tools   map[string]*ToolRow
	origins map[string]*Origin
	samples map[string][]int64
	zone    *time.Location
	n       int64
	// from and to are the query's hours, pfrom and pto the period before it's compared with.
	from, to, pfrom, pto int64
}

func newTally(q Query) *tally {
	t := &tally{q: q, r: Report{Points: q.Points()}, groups: map[string]*Group{}, models: map[string]*Model{},
		efforts: map[string]*Effort{}, tools: map[string]*ToolRow{}, origins: map[string]*Origin{}, samples: map[string][]int64{},
		zone: time.FixedZone("", q.Offset)}
	t.n = int64(len(t.r.Points))
	t.from, t.to = q.From.Unix()/3600, (q.To.Unix()+3599)/3600
	plen := q.To.Sub(q.From)
	t.pfrom, t.pto = q.From.Add(-plen).Unix()/3600, t.from
	return t
}

// partsOf is how a slot of s's is shared between the query's groups.
func (t *tally) partsOf(s Session, shares []Share) func(sl *Slot, w float64) []part {
	q := t.q
	switch q.Group {
	case ByRepo:
		return func(sl *Slot, _ float64) []part {
			var out []part
			for _, sh := range s.charged(sl, shares, q.Split) {
				if q.has("repo", sh.Repo) {
					out = append(out, part{key: sh.Repo, label: sh.Repo, w: sh.Share})
				}
			}
			if len(shares) == 0 && len(q.Filters["repo"]) == 0 {
				out = append(out, part{key: "", label: "no repo", w: 1})
			}
			return out
		}
	case BySession:
		var repos []string
		for _, rp := range s.Repos {
			repos = append(repos, rp.Name)
		}
		return func(_ *Slot, w float64) []part {
			return []part{{key: s.ID, label: cmpOr(s.Name, s.ID), repos: repos, origin: originOf(s), w: w}}
		}
	case ByOrigin:
		return func(_ *Slot, w float64) []part {
			return []part{{key: originOf(s), label: originLabel(originOf(s)), w: w}}
		}
	case ByKind:
		return func(_ *Slot, w float64) []part { return []part{{key: s.Kind, label: cmpOr(s.Kind, "none"), w: w}} }
	case ByProvider:
		return func(_ *Slot, w float64) []part {
			return []part{{key: s.Provider, label: cmpOr(s.Provider, "none"), w: w}}
		}
	case ByAccount:
		return func(sl *Slot, w float64) []part {
			a := s.accountAt(sl.Hour)
			return []part{{key: a.ID, label: cmpOr(a.Name, a.ID, "unknown account"), w: w}}
		}
	case ByModel:
		return func(sl *Slot, w float64) []part {
			if sl.Model == "" {
				return nil
			}
			return []part{{key: sl.Model, label: sl.Model, w: w}}
		}
	case ByDay:
		return func(sl *Slot, w float64) []part {
			d := time.Unix(sl.Hour*3600, 0).In(t.zone).Format("2006-01-02")
			return []part{{key: d, label: d, w: w}}
		}
	}
	return nil
}

// session adds a session's slots and tool calls, at its weight in the query.
func (t *tally) session(s Session) {
	q := t.q
	shares := s.Shares()
	w := q.weight(s, shares)
	if w == 0 {
		return
	}
	parts := t.partsOf(s, shares)
	o := t.origins[originOf(s)]
	if o == nil {
		o = &Origin{Name: originOf(s)}
		t.origins[o.Name] = o
	}
	counted, prev := false, false
	span, pspan := spans{}, spans{}
	for _, sc := range s.Scans {
		for _, sl := range sc.Slots {
			w := q.slotWeight(s, sl, shares, w)
			if w == 0 || !q.keeps(s, sl.Hour, sl.Model) {
				continue
			}
			if sl.Hour >= t.pfrom && sl.Hour < t.pto {
				prev = prev || sl.Requests > 0
				add(&t.r.Prev, sl, w)
				pspan.add(q, sl)
			}
			if sl.Hour < t.from || sl.Hour >= t.to {
				continue
			}
			counted = counted || sl.Requests > 0
			span.add(q, sl)
			end := time.Unix((sl.Hour+1)*3600, 0)
			if sc.Last.Before(end) {
				end = sc.Last
			}
			t.slot(s, sl, o, w, end, parts)
		}
	}
	if prev {
		t.r.Prev.Sessions++
		t.r.Prev.SpanS += round(float64(pspan.secs()) * w)
	}
	if !counted {
		return
	}
	t.r.Totals.Sessions++
	o.Sessions++
	t.r.Totals.SpanS += round(float64(span.secs()) * w)
	if len(s.Repos) > 1 {
		t.r.Totals.MultiRepo++
	}
	t.r.Totals.Pairs += max(1, len(s.Repos))
	t.toolCalls(s, w)
}

// slot adds one of s's slots in the query's period, at weight w, its requests ending by end.
func (t *tally) slot(s Session, sl *Slot, o *Origin, w float64, end time.Time, parts func(sl *Slot, w float64) []part) {
	add(&t.r.Totals, sl, w)
	o.Requests += round(float64(sl.Requests) * w)
	o.Tokens = o.Tokens.Add(scale(sl.Tokens, w), 1)
	o.Cost += sl.Costs.Total() * w
	cost := sl.Costs.Total()
	if sl.Model != "" {
		t.model(sl, w)
	}
	i := t.q.index(sl.Hour*3600) - t.q.index(t.q.From.Unix())
	for _, p := range parts(sl, w) {
		g := t.groups[p.key]
		if g == nil {
			g = &Group{Key: p.key, Label: p.label, Repos: p.repos, Origin: p.origin, Series: make([]float64, t.n),
				Used: make([]int64, t.n), sessions: map[string]bool{}}
			t.groups[p.key] = g
		}
		g.Requests += round(float64(sl.Requests) * p.w)
		g.Tokens += round(float64(sl.Tokens.Total()) * p.w)
		g.Cost += cost * p.w
		if i >= 0 && i < t.n {
			g.Series[i] += cost * p.w
			g.Used[i] += round(float64(sl.Tokens.Total()) * p.w)
		}
		if sl.Requests > 0 {
			g.sessions[s.ID] = true
			if end.After(g.Last) {
				g.Last = end
			}
		}
	}
}

// model adds a model's slot to its model, its effort and the hour of day.
func (t *tally) model(sl *Slot, w float64) {
	cost := sl.Costs.Total()
	t.r.Tokens = t.r.Tokens.Add(scale(sl.Tokens, w), 1)
	t.r.Costs = t.r.Costs.Add(sl.Costs, w)
	m := t.models[sl.Model]
	if m == nil {
		m = &Model{Name: sl.Model}
		t.models[sl.Model] = m
	}
	m.Requests += round(float64(sl.Requests) * w)
	m.Sidechain += round(float64(sl.Sidechain) * w)
	m.Tokens += round(float64(sl.Tokens.Total()) * w)
	m.Cost += cost * w
	if sl.Effort != "" {
		e := t.efforts[sl.Effort]
		if e == nil {
			e = &Effort{Name: sl.Effort}
			t.efforts[sl.Effort] = e
		}
		e.Requests += round(float64(sl.Requests) * w)
		e.Cost += cost * w
	}
	t.r.Hours[time.Unix(sl.Hour*3600, 0).In(t.zone).Hour()] += cost * w
}

// toolCalls adds a counted session's tool calls in the query's period.
func (t *tally) toolCalls(s Session, w float64) {
	for _, sc := range s.Scans {
		used := map[string]bool{}
		for _, ts := range sc.ToolSlots {
			if ts.Hour < t.from || ts.Hour >= t.to || !t.q.keeps(s, ts.Hour, ts.Model) {
				continue
			}
			row := t.tools[ts.Name]
			if row == nil {
				row = &ToolRow{Name: ts.Name}
				t.tools[ts.Name] = row
			}
			row.Calls += round(float64(ts.Calls) * w)
			row.Errors += round(float64(ts.Errors) * w)
			for i, b := range ts.Buckets {
				row.Buckets[i] += round(float64(b) * w)
			}
			used[ts.Name] = true
		}
		for name := range used {
			if tl := sc.Tools[name]; tl != nil {
				t.samples[name] = append(t.samples[name], tl.Samples...)
			}
		}
	}
}

// report is the tally, its rows in order.
func (t *tally) report() Report {
	r := t.r
	for _, g := range t.groups {
		g.Sessions = len(g.sessions)
		if g.Requests > 0 || g.Cost > 0 {
			r.Groups = append(r.Groups, *g)
		}
	}
	slices.SortFunc(r.Groups, func(a, b Group) int {
		if t.q.Group == ByDay {
			return strings.Compare(a.Key, b.Key)
		}
		return cmp.Or(cmp.Compare(b.Cost, a.Cost), strings.Compare(a.Key, b.Key))
	})
	for _, name := range []string{OriginOriel, OriginOutside} {
		if o := t.origins[name]; o != nil && o.Requests > 0 {
			r.Origins = append(r.Origins, *o)
		}
	}
	for _, m := range t.models {
		if m.Requests > 0 {
			r.Models = append(r.Models, *m)
		}
	}
	slices.SortFunc(r.Models, func(a, b Model) int { return cmp.Or(cmp.Compare(b.Cost, a.Cost), strings.Compare(a.Name, b.Name)) })
	for _, e := range t.efforts {
		r.Efforts = append(r.Efforts, *e)
	}
	SortEfforts(r.Efforts)
	for name, tr := range t.tools {
		if tr.Calls == 0 {
			continue
		}
		tr.P50 = median(t.samples[name])
		r.Tools = append(r.Tools, *tr)
	}
	slices.SortFunc(r.Tools, func(a, b ToolRow) int { return cmp.Or(cmp.Compare(b.Calls, a.Calls), strings.Compare(a.Name, b.Name)) })
	return r
}

// keeps says whether q counts session s's hour on model ("" for what isn't a model's: turns, active time): the
// model filter, and the account the session ran on then.
func (q Query) keeps(s Session, hour int64, model string) bool {
	return (model == "" || q.has("model", model)) && q.has("account", s.accountAt(hour).ID)
}

// spans are a session's first and last record on each day of the query's zone, over the hours added to them.
type spans map[int64][2]int64

func (p spans) add(q Query, sl *Slot) {
	if sl.Model != "" || sl.Last == 0 {
		return
	}
	day := Query{Offset: q.Offset}.index(sl.Hour * 3600)
	v, ok := p[day]
	if !ok || sl.First < v[0] {
		v[0] = sl.First
	}
	v[1] = max(v[1], sl.Last)
	p[day] = v
}

func (p spans) secs() int64 {
	var n int64
	for _, v := range p {
		n += v[1] - v[0]
	}
	return n
}

func add(t *Totals, sl *Slot, w float64) {
	t.Requests += round(float64(sl.Requests) * w)
	t.Turns += round(float64(sl.Turns) * w)
	t.ToolCalls += round(float64(sl.ToolCalls) * w)
	t.ToolErrors += round(float64(sl.ToolErrors) * w)
	t.ActiveS += round(float64(sl.ActiveS) * w)
	t.Tokens = t.Tokens.Add(scale(sl.Tokens, w), 1)
	t.Cost += sl.Costs.Total() * w
}

func scale(t Tokens, w float64) Tokens {
	if w == 1 {
		return t
	}
	f := func(n int64) int64 { return round(float64(n) * w) }
	return Tokens{Input: f(t.Input), Output: f(t.Output), CacheRead: f(t.CacheRead), CacheWrite5m: f(t.CacheWrite5m), CacheWrite1h: f(t.CacheWrite1h)}
}

func round(f float64) int64 {
	if f < 0 {
		return -int64(-f + .5)
	}
	return int64(f + .5)
}

// effortOrder is the order the APIs define the levels in, low to high.
var effortOrder = map[string]int{"minimal": 0, "low": 1, "medium": 2, "high": 3, "xhigh": 4, "max": 5}

// SortEfforts puts efforts low to high, unknown ones after by name.
func SortEfforts(es []Effort) {
	slices.SortFunc(es, func(a, b Effort) int {
		x, xok := effortOrder[a.Name]
		y, yok := effortOrder[b.Name]
		switch {
		case xok && yok:
			return cmp.Compare(x, y)
		case xok != yok:
			if xok {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
}

func median(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[len(s)/2]
}

// Detail is one session's stats, for its page.
type Detail struct {
	Session  string `json:"session"`
	Name     string `json:"name,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	Account  string `json:"account,omitempty"` // the latest it ran on
	// AccountName is how the account reads: an email or an organisation.
	AccountName string    `json:"accountName,omitempty"`
	Live        bool      `json:"live,omitempty"`
	Origin      string    `json:"origin,omitempty"`
	First       time.Time `json:"first,omitzero"`
	Last        time.Time `json:"last,omitzero"`
	Totals      Totals    `json:"totals"`
	Peak        int64     `json:"peak"`
	Window      int64     `json:"window,omitempty"`
	Agents      int       `json:"agents,omitempty"` // subagents
	// Buckets are its five-minute steps with any work, oldest first.
	Buckets     []Bucket     `json:"buckets"`
	Compactions []Compaction `json:"compactions,omitempty"`
	Repos       []Share      `json:"repos,omitempty"`
	Models      []Model      `json:"models"`
	Tools       []ToolRow    `json:"tools"`
	Hours       [24]float64  `json:"hours"`
	Efforts     []Effort     `json:"efforts"`
	Costs       Costs        `json:"costs"`
	Prices      Prices       `json:"prices,omitzero"`
}

// Detailed is s's stats, its days and hours in the zone offset seconds east of UTC. Each repo's share is of the cost
// charged to it (Session.charged); split charges it that, otherwise the whole session.
func Detailed(s Session, split bool, offset int) Detail {
	q := Query{Group: ByModel, From: earliest(s), To: latest(s).Add(time.Hour), Offset: offset}
	sum := Run(q, []Session{s})
	q.Group, q.Split = ByRepo, true
	repos := map[string]float64{}
	for _, g := range Run(q, []Session{s}).Groups {
		repos[g.Key] = g.Cost
	}
	var acct Account
	if n := len(s.Accounts); n > 0 {
		acct = s.Accounts[n-1]
	}
	d := Detail{Session: s.ID, Name: s.Name, Provider: s.Provider, Model: s.Model, Effort: s.Effort, Live: s.Live, Origin: originOf(s),
		Account: acct.ID, AccountName: acct.Name,
		Totals: sum.Totals, Models: sum.Models, Tools: sum.Tools, Hours: sum.Hours, Efforts: sum.Efforts, Costs: sum.Costs}
	buckets := map[int64]*Bucket{}
	for _, sc := range s.Scans {
		if d.First.IsZero() || !sc.First.IsZero() && sc.First.Before(d.First) {
			d.First = sc.First
		}
		if sc.Last.After(d.Last) {
			d.Last = sc.Last
		}
		d.Peak = max(d.Peak, sc.Peak)
		d.Window = max(d.Window, sc.Window)
		d.Agents += len(sc.Agents)
		d.Compactions = append(d.Compactions, sc.Compactions...)
		for k, b := range sc.Buckets {
			x := buckets[k]
			if x == nil {
				x = &Bucket{At: k}
				buckets[k] = x
			}
			x.Requests += b.Requests
			x.Turns += b.Turns
			x.Costs = x.Costs.Add(b.Costs, 1)
			x.Tokens = x.Tokens.Add(b.Tokens, 1)
			x.Context = max(x.Context, b.Context)
		}
	}
	for _, b := range buckets {
		d.Buckets = append(d.Buckets, *b)
	}
	slices.SortFunc(d.Buckets, func(a, b Bucket) int { return cmp.Compare(a.At, b.At) })
	slices.SortFunc(d.Compactions, func(a, b Compaction) int { return a.At.Compare(b.At) })
	for _, sh := range s.Shares() {
		if sum.Totals.Cost > 0 {
			sh.Share = repos[sh.Repo] / sum.Totals.Cost
		}
		sh.Cost = sum.Totals.Cost
		if split {
			sh.Cost = repos[sh.Repo]
		}
		d.Repos = append(d.Repos, sh)
	}
	slices.SortStableFunc(d.Repos, func(a, b Share) int { return cmp.Compare(b.Share, a.Share) })
	return d
}

func earliest(s Session) time.Time {
	var t time.Time
	for _, sc := range s.Scans {
		if !sc.First.IsZero() && (t.IsZero() || sc.First.Before(t)) {
			t = sc.First
		}
	}
	return t.Truncate(time.Hour)
}

func latest(s Session) time.Time {
	var t time.Time
	for _, sc := range s.Scans {
		if sc.Last.After(t) {
			t = sc.Last
		}
	}
	return t
}
