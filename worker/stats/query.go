package stats

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Session is a session as a query sees it: what it is, the repos it works in, and its logs' scans.
type Session struct {
	ID       string
	Name     string
	Kind     string
	Provider string
	Model    string
	Effort   string
	Worker   string
	// Account is the provider's account the session ran on (its plan), and AccountName how it reads.
	Account     string
	AccountName string
	Live        bool // its agent is running
	Repos       []Repo
	Scans       []*Scan
}

// Repo is one of a session's projects: its name, and the directory it's checked out in for the session.
type Repo struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

// Share is a repo's part of a session: its share of the file tool calls that fell in a repo, and their count.
type Share struct {
	Repo  string  `json:"repo"`
	Share float64 `json:"share"`
	Files int64   `json:"files"`
	Cost  float64 `json:"cost"`
}

// Shares are each repo's share of the session, by its edits and file tool calls (FileTools); a session with no file
// work splits evenly. A session with no repo has none.
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
			best := -1
			for i, r := range s.Repos {
				if r.Dir != "" && within(p, r.Dir) && (best < 0 || len(r.Dir) > len(s.Repos[best].Dir)) {
					best = i
				}
			}
			if best >= 0 {
				out[best].Files += n
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
)

// Query asks for the stats of a range, grouped one way and filtered.
type Query struct {
	Group string    `json:"group,omitempty"` // ByRepo when ""
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
	Step  string    `json:"step,omitempty"` // "day" (the default) or "hour": the series' resolution
	// Offset is the zone the days and hours of the day are in, in seconds east of UTC.
	Offset int `json:"offset,omitempty"`
	// Filters keep what matches any of a key's values: repo, session, kind, model, provider, account, worker.
	Filters map[string][]string `json:"filters,omitempty"`
	// Split charges a multi-repo session to each repo by its share (Shares); otherwise each repo is charged the whole.
	Split bool `json:"split,omitempty"`
}

// Totals are a range's headline numbers.
type Totals struct {
	Sessions   int     `json:"sessions"`
	MultiRepo  int     `json:"multiRepo,omitempty"` // sessions with more than one repo
	Pairs      int     `json:"pairs,omitempty"`     // repo-sessions: each session once for each of its repos
	Requests   int64   `json:"requests"`
	Turns      int64   `json:"turns"`
	ToolCalls  int64   `json:"toolCalls"`
	ToolErrors int64   `json:"toolErrors"`
	ActiveS    int64   `json:"activeS"`
	Tokens     Tokens  `json:"tokens"`
	Cost       float64 `json:"cost"`
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
	Series   []float64 `json:"series"` // cost at each of the report's points
	Used     []int64   `json:"used"`   // tokens at each point
	sessions map[string]bool
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
	Worker  string      `json:"worker,omitempty"`
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
// the repos it keeps (or all of it, unsplit).
func (q Query) weight(s Session, shares []Share) float64 {
	if !q.has("session", s.ID) || !q.has("kind", s.Kind) || !q.has("provider", s.Provider) || !q.has("worker", s.Worker) ||
		!q.has("account", s.Account) {
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

type part struct {
	key, label string
	repos      []string
	w          float64
}

// Run answers q from sessions.
func Run(q Query, sessions []Session) Report {
	if q.Group == "" {
		q.Group = ByRepo
	}
	r := Report{Points: q.Points()}
	groups := map[string]*Group{}
	models := map[string]*Model{}
	efforts := map[string]*Effort{}
	tools := map[string]*ToolRow{}
	var samples = map[string][]int64{}
	zone := time.FixedZone("", q.Offset)
	n := int64(len(r.Points))
	from, to := q.From.Unix()/3600, (q.To.Unix()+3599)/3600
	plen := q.To.Sub(q.From)
	pfrom, pto := q.From.Add(-plen).Unix()/3600, from
	for _, s := range sessions {
		shares := s.Shares()
		w := q.weight(s, shares)
		if w == 0 {
			continue
		}
		var parts func(model string, at int64) []part
		switch q.Group {
		case ByRepo:
			parts = func(string, int64) []part {
				var out []part
				for _, sh := range shares {
					if !q.has("repo", sh.Repo) {
						continue
					}
					pw := 1.0
					if q.Split {
						pw = sh.Share
					}
					out = append(out, part{key: sh.Repo, label: sh.Repo, w: pw})
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
			parts = func(string, int64) []part { return []part{{key: s.ID, label: cmpOr(s.Name, s.ID), repos: repos, w: w}} }
		case ByKind:
			parts = func(string, int64) []part { return []part{{key: s.Kind, label: cmpOr(s.Kind, "none"), w: w}} }
		case ByProvider:
			parts = func(string, int64) []part { return []part{{key: s.Provider, label: cmpOr(s.Provider, "none"), w: w}} }
		case ByAccount:
			parts = func(string, int64) []part {
				return []part{{key: s.Account, label: cmpOr(s.AccountName, s.Account, "unknown account"), w: w}}
			}
		case ByModel:
			parts = func(model string, _ int64) []part {
				if model == "" {
					return nil
				}
				return []part{{key: model, label: model, w: w}}
			}
		case ByDay:
			parts = func(_ string, hour int64) []part {
				d := time.Unix(hour*3600, 0).In(zone).Format("2006-01-02")
				return []part{{key: d, label: d, w: w}}
			}
		}
		counted, prev := false, false
		for _, sc := range s.Scans {
			for _, sl := range sc.Slots {
				if sl.Hour >= pfrom && sl.Hour < pto && (sl.Model == "" || q.has("model", sl.Model)) {
					if sl.Requests > 0 {
						prev = true
					}
					add(&r.Prev, sl, w)
				}
				if sl.Hour < from || sl.Hour >= to || sl.Model != "" && !q.has("model", sl.Model) {
					continue
				}
				if sl.Requests > 0 {
					counted = true
				}
				end := time.Unix((sl.Hour+1)*3600, 0)
				if sc.Last.Before(end) {
					end = sc.Last
				}
				add(&r.Totals, sl, w)
				cost := sl.Costs.Total()
				if sl.Model != "" {
					r.Tokens = r.Tokens.Add(scale(sl.Tokens, w), 1)
					r.Costs = r.Costs.Add(sl.Costs, w)
					m := models[sl.Model]
					if m == nil {
						m = &Model{Name: sl.Model}
						models[sl.Model] = m
					}
					m.Requests += round(float64(sl.Requests) * w)
					m.Sidechain += round(float64(sl.Sidechain) * w)
					m.Tokens += round(float64(sl.Tokens.Total()) * w)
					m.Cost += cost * w
					if sl.Effort != "" {
						e := efforts[sl.Effort]
						if e == nil {
							e = &Effort{Name: sl.Effort}
							efforts[sl.Effort] = e
						}
						e.Requests += round(float64(sl.Requests) * w)
						e.Cost += cost * w
					}
					r.Hours[time.Unix(sl.Hour*3600, 0).In(zone).Hour()] += cost * w
				}
				i := q.index(sl.Hour*3600) - q.index(q.From.Unix())
				for _, p := range parts(sl.Model, sl.Hour) {
					g := groups[p.key]
					if g == nil {
						g = &Group{Key: p.key, Label: p.label, Repos: p.repos, Series: make([]float64, n), Used: make([]int64, n),
							sessions: map[string]bool{}}
						groups[p.key] = g
					}
					g.Requests += round(float64(sl.Requests) * p.w)
					g.Tokens += round(float64(sl.Tokens.Total()) * p.w)
					g.Cost += cost * p.w
					if i >= 0 && i < n {
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
		}
		if prev {
			r.Prev.Sessions++
		}
		if !counted {
			continue
		}
		r.Totals.Sessions++
		if len(s.Repos) > 1 {
			r.Totals.MultiRepo++
		}
		r.Totals.Pairs += max(1, len(s.Repos))
		for _, sc := range s.Scans {
			for name, t := range sc.Tools {
				row := tools[name]
				if row == nil {
					row = &ToolRow{Name: name}
					tools[name] = row
				}
				row.Calls += round(float64(t.Calls) * w)
				row.Errors += round(float64(t.Errors) * w)
				for i, b := range t.Buckets {
					row.Buckets[i] += round(float64(b) * w)
				}
				samples[name] = append(samples[name], t.Samples...)
			}
		}
	}
	for _, g := range groups {
		g.Sessions = len(g.sessions)
		if g.Requests > 0 || g.Cost > 0 {
			r.Groups = append(r.Groups, *g)
		}
	}
	slices.SortFunc(r.Groups, func(a, b Group) int {
		if q.Group == ByDay {
			return strings.Compare(a.Key, b.Key)
		}
		return cmp.Or(cmp.Compare(b.Cost, a.Cost), strings.Compare(a.Key, b.Key))
	})
	for _, m := range models {
		if m.Requests > 0 {
			r.Models = append(r.Models, *m)
		}
	}
	slices.SortFunc(r.Models, func(a, b Model) int { return cmp.Or(cmp.Compare(b.Cost, a.Cost), strings.Compare(a.Name, b.Name)) })
	for _, e := range efforts {
		r.Efforts = append(r.Efforts, *e)
	}
	SortEfforts(r.Efforts)
	for name, t := range tools {
		t.P50 = median(samples[name])
		r.Tools = append(r.Tools, *t)
	}
	slices.SortFunc(r.Tools, func(a, b ToolRow) int { return cmp.Or(cmp.Compare(b.Calls, a.Calls), strings.Compare(a.Name, b.Name)) })
	return r
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
	Worker   string `json:"worker,omitempty"`
	Account  string `json:"account,omitempty"`
	// AccountName is how the account reads: an email or an organisation.
	AccountName string    `json:"accountName,omitempty"`
	Live        bool      `json:"live,omitempty"`
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
}

// Detailed is s's stats, its days and hours in the zone offset seconds east of UTC. split charges each repo its share;
// otherwise each is charged the whole session.
func Detailed(s Session, split bool, offset int) Detail {
	sum := Run(Query{Group: ByModel, From: earliest(s), To: latest(s).Add(time.Hour), Offset: offset}, []Session{s})
	d := Detail{Session: s.ID, Name: s.Name, Provider: s.Provider, Model: s.Model, Effort: s.Effort, Worker: s.Worker, Live: s.Live,
		Account: s.Account, AccountName: s.AccountName,
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
		sh.Cost = sum.Totals.Cost
		if split {
			sh.Cost *= sh.Share
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
