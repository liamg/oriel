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
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/atomicfile"
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
	if err == nil {
		_, err = f.Write(append(b, '\n'))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		s.env.Log.Warn("plan usage reading not kept", "path", path, "err", err)
	}
}

// pruneUsage drops the readings older than UsageKeep.
func (s *Stats) pruneUsage(path string) {
	keep := readUsage(path, "", s.env.Now().Add(-UsageKeep))
	var buf bytes.Buffer
	for _, u := range keep {
		b, _ := json.Marshal(u)
		buf.Write(append(b, '\n'))
	}
	if err := atomicfile.Write(path, buf.Bytes(), 0o644); err != nil {
		s.env.Log.Warn("plan usage readings not pruned", "path", path, "err", err)
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
	defer s.env.Recover("UsageHistory", nil)
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	return readUsage(s.usagePath(), r.Provider, r.Since)
}

// ReadUsage asks every provider for its plan's windows now, all at once, and reports each that's newer than the one
// held.
func (s *Stats) ReadUsage(ctx context.Context) (err error) {
	defer s.env.Recover("ReadUsage", &err)
	ps := s.providers.List()
	errs := make([]error, len(ps))
	var wg sync.WaitGroup
	for i, p := range ps {
		wg.Go(func() { errs[i] = s.readProvider(ctx, p) })
	}
	wg.Wait()
	return errors.Join(errs...)
}

// readProvider asks p for its plan's windows, saying in the state that it's being read and how that went.
func (s *Stats) readProvider(ctx context.Context, p agent.Provider) error {
	_ = s.st.Emit(core.UsageRead{Provider: p.ID(), Reading: true})
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	u, err := p.Usage(cctx)
	cancel()
	_ = s.st.Emit(core.UsageRead{Provider: p.ID(), Failure: failure(err)})
	if err != nil {
		s.env.Log.Warn(p.ID()+"'s plan windows couldn't be read", "provider", p.ID(), "err", err)
	}
	if err != nil || len(u.Windows) == 0 {
		return err
	}
	if u.Provider == "" {
		u.Provider = p.ID()
	}
	if !u.Sampled.IsZero() && slices.ContainsFunc(s.st.Snapshot().Usage, func(o core.ProviderUsage) bool {
		return o.Same(u) && !o.Sampled.Before(u.Sampled)
	}) {
		return nil
	}
	s.UsageSampled(u)
	return nil
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
