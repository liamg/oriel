package persist

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/price"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/host/stats"
)

// The host reads its sessions' logs into stats (crit's internal/stats, per session) and answers StatsQuery and
// SessionStats from them. A provider that keeps no log of its own (agent.Logged) has its events recorded by the
// host instead, in the session's directory. The stats also read every log a provider keeps on this machine
// (agent.Discovered): one that no Oriel session owns, by its path or the provider's id for it, is a session outside
// Oriel.

// StatsSessionReq asks for one session's stats.
type StatsSessionReq struct {
	Session core.SessionID `json:"session"`
	Split   bool           `json:"split,omitempty"`  // charge each repo its share; otherwise the whole session
	Offset  int            `json:"offset,omitempty"` // the app's zone, seconds east of UTC
}

// Stats are the stats and plan readings the host keeps.
type Stats struct {
	env       hostcore.Env
	st        hostcore.State
	providers *agent.Providers
	prices    func() *price.Book

	once    sync.Once
	data    *stats.Store
	mu      sync.Mutex // the stats store, and the events records it reads
	usageMu sync.Mutex
}

// NewStats keeps stats under env's home, priced with the book prices gives.
func NewStats(env hostcore.Env, st hostcore.State, providers *agent.Providers, prices func() *price.Book) *Stats {
	return &Stats{env: env, st: st, providers: providers, prices: prices}
}

// pricedBy is where the costs come from.
func (s *Stats) pricedBy() stats.Prices {
	if s.prices == nil {
		return stats.Prices{}
	}
	return stats.PricesOf(s.prices())
}

// Store is the stats' store of scanned logs.
func (s *Stats) Store() *stats.Store {
	s.once.Do(func() { s.data = stats.NewStore(filepath.Join(s.env.Home, "stats.json"), s.prices) })
	return s.data
}

// eventsLog is where the host records a session's agent events, for a provider with no log of its own.
func (s *Stats) eventsLog(id core.SessionID) string {
	return filepath.Join(s.env.SessionDir(id), "agent", "events.jsonl")
}

// logsOf are the logs a session's stats come from: its provider's own, else the host's record.
func (s *Stats) logsOf(se core.SessionState) []stats.Log {
	if se.Agent.Provider == "" {
		return nil
	}
	if p, ok := s.providers.Get(se.Agent.Provider); ok {
		if l, ok := p.(agent.Logged); ok {
			var out []stats.Log
			for _, f := range l.Logs(hostcore.AgentDir(se), se.Agent.Session) {
				out = append(out, stats.Log{Path: f.Path, Format: f.Format})
			}
			return out
		}
	}
	return []stats.Log{{Path: s.eventsLog(se.ID), Format: stats.FormatEvents}}
}

// Sessions are the host's sessions as the stats see them, their logs read up to now, then the providers' logs no
// Oriel session owns: only's alone when it's set, else those changed since since.
func (s *Stats) Sessions(only core.SessionID, since time.Time) []stats.Session {
	var out []stats.Session
	own := owned{}
	for _, se := range s.st.Snapshot().Sessions {
		logs := s.logsOf(se)
		if se.Agent.Session != "" {
			own[ownKey(se.Agent.Provider, se.Agent.Session)] = true
		}
		for _, l := range logs {
			own[filepath.Clean(l.Path)] = true
		}
		if only != "" && se.ID != only || len(logs) == 0 {
			continue
		}
		out = append(out, s.session(se, logs))
	}
	if only == "" || len(out) == 0 {
		out = append(out, s.outside(only, since, own)...)
	}
	s.Store().Save()
	return out
}

func (s *Stats) session(se core.SessionState, logs []stats.Log) stats.Session {
	ss := stats.Session{ID: string(se.ID), Name: se.Name, Kind: string(se.Kind), Provider: se.Agent.Provider, Model: se.Agent.Model,
		Effort: se.Agent.Effort, Live: se.Agent.Running, Scans: s.Store().Scan(string(se.ID), logs),
		Origin: stats.OriginOriel}
	for _, a := range se.Agent.Accounts {
		ss.Accounts = append(ss.Accounts, stats.Account{At: a.At, ID: a.Account, Name: a.Name})
	}
	if len(ss.Accounts) == 0 {
		// One that never recorded its account: the provider's now.
		if acct, name := s.AccountOf(se.Agent.Provider); acct != "" {
			ss.Accounts = []stats.Account{{ID: acct, Name: name}}
		}
	}
	if ss.Name == "" {
		ss.Name = string(se.ID)
	}
	for _, p := range se.Projects {
		ss.Repos = append(ss.Repos, stats.Repo{Name: p.Name, Dir: p.Dir})
	}
	return ss
}

// owned are the logs Oriel's sessions read, by path and by provider and session id.
type owned map[string]bool

func ownKey(provider, session string) string { return provider + "\x00" + session }

func (o owned) has(provider string, t agent.Transcript) bool {
	if o[ownKey(provider, t.Session)] {
		return true
	}
	return slices.ContainsFunc(t.Logs, func(l agent.Log) bool { return o[filepath.Clean(l.Path)] })
}

// outsideID is an outside session's ID: its provider's, then the provider's own.
func outsideID(provider, session string) string { return provider + ":" + session }

// outside are the logs the providers keep that no Oriel session owns, read up to now: only's alone when it's set,
// else each changed since since.
func (s *Stats) outside(only core.SessionID, since time.Time, own owned) []stats.Session {
	var out []stats.Session
	for _, p := range s.providers.List() {
		d, ok := p.(agent.Discovered)
		if !ok {
			continue
		}
		for _, t := range d.Discover() {
			id := outsideID(p.ID(), t.Session)
			if len(t.Logs) == 0 || only != "" && id != string(only) || own.has(p.ID(), t) || only == "" && t.Modified.Before(since) {
				continue
			}
			var logs []stats.Log
			for _, l := range t.Logs {
				logs = append(logs, stats.Log{Path: l.Path, Format: l.Format})
			}
			ss := stats.Session{ID: id, Provider: p.ID(), Origin: stats.OriginOutside, Scans: s.Store().Scan(id, logs)}
			main := ss.Scans[0]
			ss.Name = main.Title
			if main.Cwd != "" {
				root := repoRoot(main.Cwd)
				ss.Repos = []stats.Repo{{Name: filepath.Base(root), Dir: root}}
				ss.Name = cmp.Or(ss.Name, filepath.Base(main.Cwd))
			}
			ss.Name = cmp.Or(ss.Name, strings.TrimSuffix(strings.TrimSuffix(filepath.Base(main.Path), ".zst"), ".jsonl"))
			if acct, name := s.AccountOf(p.ID()); acct != "" {
				ss.Accounts = []stats.Account{{ID: acct, Name: name}}
			}
			out = append(out, ss)
		}
	}
	return out
}

// repoRoot is the top of the work tree holding dir, or dir when there's none.
func repoRoot(dir string) string {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if gitx.IsRepo(d) {
			return d
		}
		if filepath.Dir(d) == d {
			return filepath.Clean(dir)
		}
	}
}

// AccountOf is the account a provider is signed in to, from its latest plan reading.
func (s *Stats) AccountOf(provider string) (string, string) {
	var best core.ProviderUsage
	for _, u := range s.st.Snapshot().Usage {
		if u.Provider == provider && u.Account != "" && !u.Sampled.Before(best.Sampled) {
			best = u
		}
	}
	return best.Account, best.AccountName
}

// Query answers a stats query over every session the host has.
func (s *Stats) Query(q stats.Query) stats.Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A log last written before the period the query compares with has nothing in it.
	r := stats.Run(q, s.Sessions("", q.From.Add(-q.To.Sub(q.From))))
	r.Scanned = s.env.Now().UTC()
	r.Prices = s.pricedBy()
	return r
}

// Session is one session's stats.
func (s *Stats) Session(r StatsSessionReq) (stats.Detail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.Sessions(r.Session, time.Time{})
	if len(ss) == 0 {
		if _, err := s.st.Session(r.Session); err != nil {
			return stats.Detail{}, err
		}
		return stats.Detail{Session: string(r.Session), Prices: s.pricedBy()}, nil
	}
	d := stats.Detailed(ss[0], r.Split, r.Offset)
	d.Prices = s.pricedBy()
	return d, nil
}

// Record adds an agent event to the session's record, for a provider that keeps no log of its own.
func (s *Stats) Record(se core.SessionState, ev stats.Event) {
	if p, ok := s.providers.Get(se.Agent.Provider); !ok {
		return
	} else if _, logged := p.(agent.Logged); logged {
		return
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.eventsLog(se.ID)
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}
