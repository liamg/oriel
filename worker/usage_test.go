package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
)

// usageStub is a provider whose plan reading the test sets, counting the times it's asked.
type usageStub struct {
	*agenttest.Fake
	mu    sync.Mutex
	u     core.ProviderUsage
	reads int
}

func newUsageStub(id string) *usageStub {
	f := agenttest.NewFake()
	f.Name = id
	return &usageStub{Fake: f}
}

func (s *usageStub) Usage(context.Context) (core.ProviderUsage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	return s.u, nil
}

func (s *usageStub) set(u core.ProviderUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.u = u
}

func (s *usageStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

func usedNow(w *Worker, provider string) float64 {
	for _, u := range w.store.State().Usage {
		if u.Provider == provider && len(u.Windows) > 0 {
			return u.Windows[0].Used
		}
	}
	return -1
}

// A provider asked again may answer with a reading it already gave (Claude's, between probes): a newer one the
// worker holds, from a session, stays, and the old one isn't kept twice.
func TestReadUsageKeepsTheNewestReading(t *testing.T) {
	claude := newUsageStub("claude")
	r := newRig(t, claude)
	probed := core.ProviderUsage{Provider: "claude", Account: "u1", Sampled: r.now.Add(-time.Hour), Windows: []core.UsageWindow{{Name: "weekly", Used: .3}}}
	claude.set(probed)
	if err := r.w.ReadUsage(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.w.usageSampled(core.ProviderUsage{Provider: "claude", Account: "u1", Sampled: r.now, Windows: []core.UsageWindow{{Name: "weekly", Used: .5}}})
	if err := r.w.ReadUsage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := usedNow(r.w, "claude"); got != .5 {
		t.Errorf("an older reading replaced the session's: %v", got)
	}
	if h := r.w.UsageHistory(UsageHistoryReq{Provider: "claude"}); len(h) != 2 {
		t.Errorf("history = %+v", h)
	}
	probed.Sampled, probed.Windows = r.now.Add(time.Minute), []core.UsageWindow{{Name: "weekly", Used: .6}}
	claude.set(probed)
	if err := r.w.ReadUsage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := usedNow(r.w, "claude"); got != .6 {
		t.Errorf("a newer reading: %v", got)
	}
}
