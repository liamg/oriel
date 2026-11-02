package claude

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
)

// TestLive runs the real claude, on the subscription, through the same harness. ORIEL_LIVE=1 runs it.
func TestLive(t *testing.T) {
	if os.Getenv("ORIEL_LIVE") != "1" {
		t.Skip("ORIEL_LIVE=1 runs the real claude")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := agenttest.Harness{
		Provider: &Provider{OrielBin: exe},
		Spec: func(t *testing.T, dir string) agent.StartSpec {
			return agent.StartSpec{StateDir: t.TempDir(), Model: "haiku"}
		},
		Timeout: 3 * time.Minute,
	}
	t.Run("start and send", func(t *testing.T) {
		s := agenttest.Start(t, h)
		s.Wait("ready", func(ev agent.Event) bool { _, ok := ev.(agent.Ready); return ok })
		s.Send("Reply with exactly the word hello and nothing else.")
		s.Wait("the turn to start", func(ev agent.Event) bool { _, ok := ev.(agent.TurnStart); return ok })
		if got := s.Reply(); !strings.Contains(strings.ToLower(got), "hello") {
			t.Errorf("reply = %q", got)
		}
		if s.S.ID() == "" {
			t.Error("no id to resume by")
		}
	})
	t.Run("fork", func(t *testing.T) {
		s := agenttest.Start(t, h)
		s.Wait("ready", func(ev agent.Event) bool { _, ok := ev.(agent.Ready); return ok })
		s.Send("Reply with exactly the word hello and nothing else.")
		s.Reply()
		ctx, cancel := context.WithTimeout(context.Background(), h.Timeout)
		defer cancel()
		got, err := s.S.Fork(ctx, agent.ForkSpec{Prompt: "Repeat this back exactly: why decimal?"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.ToLower(got), "why decimal") {
			t.Errorf("fork = %q", got)
		}
		s.Send("What was the exact text of my first message? Quote it.")
		if got := s.Reply(); !strings.Contains(strings.ToLower(got), "hello") {
			t.Errorf("after a fork, the main conversation remembers %q", got)
		}
	})
}

// TestLiveUsage reads the plan's windows through a hidden real claude's /usage. ORIEL_LIVE=1 runs it.
func TestLiveUsage(t *testing.T) {
	if os.Getenv("ORIEL_LIVE") != "1" {
		t.Skip("ORIEL_LIVE=1 runs the real claude")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	u, err := (&Provider{OrielBin: exe, ProbeDir: t.TempDir()}).probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", u)
	weekly := false
	for _, w := range u.Windows {
		weekly = weekly || w.Name == "Current week (all models)"
		if w.Resets.IsZero() || w.Length == 0 {
			t.Errorf("window = %+v", w)
		}
	}
	if !weekly {
		t.Errorf("no weekly window: %+v", u.Windows)
	}
}

// ORIEL_LIVE=1: the plan's windows from the real usage API, with this machine's Claude sign-in.
func TestLiveUsageAPI(t *testing.T) {
	if os.Getenv("ORIEL_LIVE") != "1" {
		t.Skip("set ORIEL_LIVE=1 to read the real usage API")
	}
	u, ok, err := (&Provider{}).apiUsage(context.Background(), http.DefaultClient, UsageAPI)
	if !ok || err != nil || len(u.Windows) == 0 {
		t.Fatalf("ok %v, err %v, usage %+v", ok, err, u)
	}
	for _, w := range u.Windows {
		t.Logf("%s: %.0f%%, resets %s", w.Name, w.Used*100, w.Resets.Local().Format("Mon 15:04"))
	}
}
