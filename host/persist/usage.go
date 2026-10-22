package persist

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
)

// UsageHistoryReq asks for the plan readings a host has kept: a provider's, or every one, since a time.
type UsageHistoryReq struct {
	Provider string    `json:"provider,omitempty"`
	Since    time.Time `json:"since,omitzero"`
}

// UsageKeep is how long plan readings are kept: the Usage page shows a week.
const UsageKeep = 8 * 24 * time.Hour

func (s *Stats) usagePath() string { return filepath.Join(s.env.Home, "usage.jsonl") }

// recordUsage keeps a plan reading for the Usage page's history.
func (s *Stats) recordUsage(u core.ProviderUsage) {
	if u.Sampled.IsZero() {
		u.Sampled = s.env.Now().UTC()
	}
	b, err := json.Marshal(u)
	if err != nil {
		return
	}
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	path := s.usagePath()
	if fi, err := os.Stat(path); err == nil && fi.Size() > 4<<20 {
		s.pruneUsage(path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// pruneUsage drops the readings older than UsageKeep.
func (s *Stats) pruneUsage(path string) {
	keep := readUsage(path, "", s.env.Now().Add(-UsageKeep))
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

func readUsage(path, provider string, since time.Time) []core.ProviderUsage {
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

// RestoreUsage puts back each account's latest kept reading, for a provider that has no way to read it again on its
// own.
func (s *Stats) RestoreUsage() {
	for _, u := range readUsage(s.usagePath(), "", s.env.Now().Add(-UsageKeep)) {
		if cur := s.st.Snapshot().Usage; slices.ContainsFunc(cur, func(o core.ProviderUsage) bool { return o.Same(u) && o.Sampled.After(u.Sampled) }) {
			continue
		}
		_ = s.st.Emit(core.UsageSampled{Usage: u})
	}
}

// UsageHistory is the plan readings the host has kept, oldest first.
func (s *Stats) UsageHistory(r UsageHistoryReq) []core.ProviderUsage {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	return readUsage(s.usagePath(), r.Provider, r.Since)
}

// ReadUsage asks every provider for its plan's windows now, and reports each that's newer than the one held.
func (s *Stats) ReadUsage(ctx context.Context) error {
	var errs []error
	for _, p := range s.providers.List() {
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		u, err := p.Usage(cctx)
		cancel()
		_ = s.st.Emit(core.UsageRead{Provider: p.ID(), Failure: failure(err)})
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
		if !u.Sampled.IsZero() && slices.ContainsFunc(s.st.Snapshot().Usage, func(o core.ProviderUsage) bool {
			return o.Same(u) && !o.Sampled.Before(u.Sampled)
		}) {
			continue
		}
		s.UsageSampled(u)
	}
	return errors.Join(errs...)
}

// failure is err as the state keeps it, nil for none.
func failure(err error) *core.UsageFailure {
	if err == nil {
		return nil
	}
	f := &core.UsageFailure{Err: err.Error()}
	var p agent.UsageProbeFailed
	if errors.As(err, &p) {
		f.Probe, f.Started = true, p.Started()
	}
	return f
}

// UsageSampled reports a plan reading, and keeps it.
func (s *Stats) UsageSampled(u core.ProviderUsage) {
	if err := s.st.Emit(core.UsageSampled{Usage: u}); err == nil {
		s.recordUsage(u)
	}
}
