package worker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/worker/stats"
)

// The worker reads its sessions' logs into stats (crit's internal/stats, per session) and answers stats.query and
// stats.session from them; the app merges every worker's answer. A provider that keeps no log of its own
// (agent.Logged) has its events recorded by the worker instead, in the session's directory.

// StatsSessionReq asks for one session's stats.
type StatsSessionReq struct {
	Session core.SessionID `json:"session"`
	Split   bool           `json:"split,omitempty"`  // charge each repo its share; otherwise the whole session
	Offset  int            `json:"offset,omitempty"` // the app's zone, seconds east of UTC
}

// UsageHistoryReq asks for the plan readings a worker has kept: a provider's, or every one, since a time.
type UsageHistoryReq struct {
	Provider string    `json:"provider,omitempty"`
	Since    time.Time `json:"since,omitzero"`
}

// usageKeep is how long plan readings are kept: the Usage page shows a week.
const usageKeep = 8 * 24 * time.Hour

func (w *Worker) statsStore() *stats.Store {
	w.statsOnce.Do(func() { w.statsData = stats.NewStore(filepath.Join(w.opts.Home, "stats.json")) })
	return w.statsData
}

// eventsLog is where the worker records a session's agent events, for a provider with no log of its own.
func (w *Worker) eventsLog(id core.SessionID) string {
	return filepath.Join(w.sessionDir(id), "agent", "events.jsonl")
}

// logsOf are the logs a session's stats come from: its provider's own, else the worker's record.
func (w *Worker) logsOf(se core.SessionState) []stats.Log {
	if se.Agent.Provider == "" {
		return nil
	}
	if p, ok := w.opts.Providers.Get(se.Agent.Provider); ok {
		if l, ok := p.(agent.Logged); ok {
			var out []stats.Log
			for _, f := range l.Logs(agentDir(se), se.Agent.Session) {
				out = append(out, stats.Log{Path: f.Path, Format: f.Format})
			}
			return out
		}
	}
	return []stats.Log{{Path: w.eventsLog(se.ID), Format: stats.FormatEvents}}
}

// statsSessions are the worker's sessions as the stats see them, their logs read up to now.
func (w *Worker) statsSessions(only core.SessionID) []stats.Session {
	var out []stats.Session
	for _, se := range w.store.State().Sessions {
		if only != "" && se.ID != only {
			continue
		}
		logs := w.logsOf(se)
		if len(logs) == 0 {
			continue
		}
		s := stats.Session{ID: string(se.ID), Name: se.Name, Kind: string(se.Kind), Provider: se.Agent.Provider, Model: se.Agent.Model,
			Effort: se.Agent.Effort, Worker: string(w.opts.ID), Live: se.Agent.Running, Scans: w.statsStore().Scan(string(se.ID), logs)}
		for _, a := range se.Agent.Accounts {
			s.Accounts = append(s.Accounts, stats.Account{At: a.At, ID: a.Account, Name: a.Name})
		}
		if len(s.Accounts) == 0 {
			// One that never recorded its account: the provider's on this worker now.
			if acct, name := w.accountOf(se.Agent.Provider); acct != "" {
				s.Accounts = []stats.Account{{ID: acct, Name: name}}
			}
		}
		if s.Name == "" {
			s.Name = string(se.ID)
		}
		for _, p := range se.Projects {
			s.Repos = append(s.Repos, stats.Repo{Name: p.Name, Dir: p.Dir})
		}
		out = append(out, s)
	}
	return out
}

// accountOf is the account this worker's provider is signed in to, from its latest plan reading.
func (w *Worker) accountOf(provider string) (string, string) {
	var best core.ProviderUsage
	for _, u := range w.store.State().Usage {
		if u.Provider == provider && u.Account != "" && !u.Sampled.Before(best.Sampled) {
			best = u
		}
	}
	return best.Account, best.AccountName
}

// StatsQuery answers a stats query over every session the worker has.
func (w *Worker) StatsQuery(q stats.Query) stats.Report {
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	r := stats.Run(q, w.statsSessions(""))
	r.Worker, r.Scanned = string(w.opts.ID), w.opts.Now().UTC()
	return r
}

// SessionStats is one session's stats.
func (w *Worker) SessionStats(r StatsSessionReq) (stats.Detail, error) {
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	ss := w.statsSessions(r.Session)
	if len(ss) == 0 {
		if _, err := w.session(r.Session); err != nil {
			return stats.Detail{}, err
		}
		return stats.Detail{Session: string(r.Session)}, nil
	}
	return stats.Detailed(ss[0], r.Split, r.Offset), nil
}

// record adds an agent event to the session's record, for a provider that keeps no log of its own.
func (h *host) record(se core.SessionState, ev stats.Event) {
	w := h.w
	if p, ok := w.opts.Providers.Get(se.Agent.Provider); !ok {
		return
	} else if _, logged := p.(agent.Logged); logged {
		return
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	path := w.eventsLog(h.id)
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

// recordUsage keeps a plan reading for the Usage page's history.
func (w *Worker) recordUsage(u core.ProviderUsage) {
	if u.Sampled.IsZero() {
		u.Sampled = w.opts.Now().UTC()
	}
	b, err := json.Marshal(u)
	if err != nil {
		return
	}
	w.usageMu.Lock()
	defer w.usageMu.Unlock()
	path := filepath.Join(w.opts.Home, "usage.jsonl")
	if fi, err := os.Stat(path); err == nil && fi.Size() > 4<<20 {
		w.pruneUsage(path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// pruneUsage drops the readings older than usageKeep.
func (w *Worker) pruneUsage(path string) {
	keep := w.readUsage(path, "", w.opts.Now().Add(-usageKeep))
	var buf bytes.Buffer
	for _, u := range keep {
		b, _ := json.Marshal(u)
		buf.Write(append(b, '\n'))
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, buf.Bytes(), 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}

func (w *Worker) readUsage(path, provider string, since time.Time) []core.ProviderUsage {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []core.ProviderUsage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var u core.ProviderUsage
		if json.Unmarshal(sc.Bytes(), &u) != nil || provider != "" && u.Provider != provider || u.Sampled.Before(since) {
			continue
		}
		out = append(out, u)
	}
	return out
}

// UsageHistory is the plan readings the worker has kept, oldest first.
func (w *Worker) UsageHistory(r UsageHistoryReq) []core.ProviderUsage {
	w.usageMu.Lock()
	defer w.usageMu.Unlock()
	return w.readUsage(filepath.Join(w.opts.Home, "usage.jsonl"), r.Provider, r.Since)
}

// ReadUsage asks every provider for its plan's windows now, and reports each.
func (w *Worker) ReadUsage(ctx context.Context) error {
	var errs []error
	for _, p := range w.opts.Providers.List() {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		u, err := p.Usage(cctx)
		cancel()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if len(u.Windows) == 0 {
			continue
		}
		if u.Provider == "" {
			u.Provider = p.ID()
		}
		w.usageSampled(u)
	}
	return errors.Join(errs...)
}

// usageSampled reports a plan reading, as this worker's, and keeps it.
func (w *Worker) usageSampled(u core.ProviderUsage) {
	u.Workers = []core.WorkerID{w.opts.ID}
	if err := w.emit(core.UsageSampled{Usage: u}); err == nil {
		w.recordUsage(u)
	}
}
