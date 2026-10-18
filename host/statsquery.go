package host

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/stats"
)

// The host reads its sessions' logs into stats (crit's internal/stats, per session) and answers StatsQuery and
// SessionStats from them. A provider that keeps no log of its own (agent.Logged) has its events recorded by the
// host instead, in the session's directory.

// StatsSessionReq asks for one session's stats.
type StatsSessionReq struct {
	Session core.SessionID `json:"session"`
	Split   bool           `json:"split,omitempty"`  // charge each repo its share; otherwise the whole session
	Offset  int            `json:"offset,omitempty"` // the app's zone, seconds east of UTC
}

// UsageHistoryReq asks for the plan readings a host has kept: a provider's, or every one, since a time.
type UsageHistoryReq struct {
	Provider string    `json:"provider,omitempty"`
	Since    time.Time `json:"since,omitzero"`
}

// usageKeep is how long plan readings are kept: the Usage page shows a week.
const usageKeep = 8 * 24 * time.Hour

func (w *Host) statsStore() *stats.Store {
	w.statsOnce.Do(func() { w.statsData = stats.NewStore(filepath.Join(w.opts.Home, "stats.json")) })
	return w.statsData
}

// eventsLog is where the host records a session's agent events, for a provider with no log of its own.
func (w *Host) eventsLog(id core.SessionID) string {
	return filepath.Join(w.sessionDir(id), "agent", "events.jsonl")
}

// logsOf are the logs a session's stats come from: its provider's own, else the host's record.
func (w *Host) logsOf(se core.SessionState) []stats.Log {
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

// statsSessions are the host's sessions as the stats see them, their logs read up to now, then the providers' logs
// no Oriel session owns: only's alone when it's set, else those changed since since.
func (w *Host) statsSessions(only core.SessionID, since time.Time) []stats.Session {
	var out []stats.Session
	own := owned{}
	for _, se := range w.store.State().Sessions {
		logs := w.logsOf(se)
		if se.Agent.Session != "" {
			own[ownKey(se.Agent.Provider, se.Agent.Session)] = true
		}
		for _, l := range logs {
			own[filepath.Clean(l.Path)] = true
		}
		if only != "" && se.ID != only || len(logs) == 0 {
			continue
		}
		s := stats.Session{ID: string(se.ID), Name: se.Name, Kind: string(se.Kind), Provider: se.Agent.Provider, Model: se.Agent.Model,
			Effort: se.Agent.Effort, Live: se.Agent.Running, Scans: w.statsStore().Scan(string(se.ID), logs),
			Origin: stats.OriginOriel}
		for _, a := range se.Agent.Accounts {
			s.Accounts = append(s.Accounts, stats.Account{At: a.At, ID: a.Account, Name: a.Name})
		}
		if len(s.Accounts) == 0 {
			// One that never recorded its account: the provider's now.
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
	if only == "" || len(out) == 0 {
		out = append(out, w.outsideSessions(only, since, own)...)
	}
	return out
}

// accountOf is the account a provider is signed in to, from its latest plan reading.
func (w *Host) accountOf(provider string) (string, string) {
	var best core.ProviderUsage
	for _, u := range w.store.State().Usage {
		if u.Provider == provider && u.Account != "" && !u.Sampled.Before(best.Sampled) {
			best = u
		}
	}
	return best.Account, best.AccountName
}

// StatsQuery answers a stats query over every session the host has.
func (w *Host) StatsQuery(q stats.Query) stats.Report {
	defer w.recover("StatsQuery", nil)
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	// A log last written before the period the query compares with has nothing in it.
	r := stats.Run(q, w.statsSessions("", q.From.Add(-q.To.Sub(q.From))))
	r.Scanned = w.opts.Now().UTC()
	return r
}

// SessionStats is one session's stats.
func (w *Host) SessionStats(r StatsSessionReq) (_ stats.Detail, err error) {
	defer w.recover("SessionStats", &err)
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	ss := w.statsSessions(r.Session, time.Time{})
	if len(ss) == 0 {
		if _, err := w.session(r.Session); err != nil {
			return stats.Detail{}, err
		}
		return stats.Detail{Session: string(r.Session)}, nil
	}
	return stats.Detailed(ss[0], r.Split, r.Offset), nil
}

// record adds an agent event to the session's record, for a provider that keeps no log of its own.
func (h *session) record(se core.SessionState, ev stats.Event) {
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
func (w *Host) recordUsage(u core.ProviderUsage) {
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
func (w *Host) pruneUsage(path string) {
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

func (w *Host) readUsage(path, provider string, since time.Time) []core.ProviderUsage {
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

// restoreUsage puts back each account's latest kept reading, for a provider that has no way to read it again on its own.
func (w *Host) restoreUsage() {
	for _, u := range w.readUsage(filepath.Join(w.opts.Home, "usage.jsonl"), "", w.opts.Now().Add(-usageKeep)) {
		if cur := w.store.State().Usage; slices.ContainsFunc(cur, func(o core.ProviderUsage) bool { return o.Same(u) && o.Sampled.After(u.Sampled) }) {
			continue
		}
		_ = w.emit(core.UsageSampled{Usage: u})
	}
}

// UsageHistory is the plan readings the host has kept, oldest first.
func (w *Host) UsageHistory(r UsageHistoryReq) []core.ProviderUsage {
	defer w.recover("UsageHistory", nil)
	w.usageMu.Lock()
	defer w.usageMu.Unlock()
	return w.readUsage(filepath.Join(w.opts.Home, "usage.jsonl"), r.Provider, r.Since)
}

// ReadUsage asks every provider for its plan's windows now, and reports each that's newer than the one held.
func (w *Host) ReadUsage(ctx context.Context) (err error) {
	defer w.recover("ReadUsage", &err)
	var errs []error
	for _, p := range w.opts.Providers.List() {
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
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
		if !u.Sampled.IsZero() && slices.ContainsFunc(w.store.State().Usage, func(o core.ProviderUsage) bool {
			return o.Same(u) && !o.Sampled.Before(u.Sampled)
		}) {
			continue
		}
		w.usageSampled(u)
	}
	return errors.Join(errs...)
}

// usageSampled reports a plan reading, and keeps it.
func (w *Host) usageSampled(u core.ProviderUsage) {
	if err := w.emit(core.UsageSampled{Usage: u}); err == nil {
		w.recordUsage(u)
	}
}
