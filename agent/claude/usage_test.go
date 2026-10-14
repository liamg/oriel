package claude

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
)

// usageProbe is the provider with the fake claude, signed in to the fixture account; runs counts the claudes started,
// and those that quit when asked.
func usageProbe(t *testing.T) (p Provider, runs func() (started, quit int)) {
	t.Helper()
	resetUsage()
	t.Cleanup(resetUsage)
	p = fakeProvider(t)
	env := fixtureConfig(t, nil)
	t.Setenv("CLAUDE_CONFIG_DIR", strings.TrimPrefix(env[0], "CLAUDE_CONFIG_DIR="))
	t.Setenv("ORIEL_FAKE_CLAUDE", "1")
	file := filepath.Join(t.TempDir(), "runs")
	t.Setenv("ORIEL_FAKE_RUNS", file)
	p.ProbeDir = filepath.Join(t.TempDir(), "probe")
	return p, func() (int, int) {
		b, _ := os.ReadFile(file)
		return bytes.Count(b, []byte("run\n")), bytes.Count(b, []byte("quit\n"))
	}
}

func resetUsage() {
	usage.mu.Lock()
	usage.last, usage.probing = core.ProviderUsage{}, false
	usage.mu.Unlock()
}

// trustedHere is whether .claude.json trusts dir, as Claude Code reads it.
func trustedHere(dir string) bool {
	m, err := loadFile(statePath(nil))
	if err != nil {
		return false
	}
	projects, _ := m["projects"].(map[string]any)
	p, _ := projects[dir].(map[string]any)
	return p["hasTrustDialogAccepted"] == true
}

// The fake's $.session.usage() once /usage has run: five_hour, 12.5%, resetting 2026-10-14T18:00:00Z.
func checkProbed(t *testing.T, u core.ProviderUsage) {
	t.Helper()
	if u.Provider != "claude" || len(u.Windows) != 1 || u.Windows[0].Name != "Current session" || u.Windows[0].Used != .125 ||
		!u.Windows[0].Resets.Equal(time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC)) || u.Windows[0].Length != 5*time.Hour || u.Sampled.IsZero() {
		t.Errorf("usage = %+v", u)
	}
	if u.AccountName != "ada@acme.example" || u.Plan != "Claude Max" || !strings.Contains(u.Source, "$.session.usage()") {
		t.Errorf("account, plan and source = %q %q %q", u.AccountName, u.Plan, u.Source)
	}
}

// With no session running, Usage starts a hidden one in Oriel's own folder, trusted first, types /usage, has the mod
// report $.session.usage(), and quits it.
func TestUsageWithoutASession(t *testing.T) {
	p, runs := usageProbe(t)
	u, err := p.Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checkProbed(t, u)
	if !trustedHere(p.ProbeDir) {
		t.Error("the probe's folder isn't trusted")
	}
	if started, quit := runs(); started != 1 || quit != 1 {
		t.Errorf("%d claudes started, %d quit when asked", started, quit)
	}
}

// A call while a probe runs has the last reading, and starts no second claude.
func TestUsageNeverProbesTwiceAtOnce(t *testing.T) {
	p, runs := usageProbe(t)
	first := make(chan error, 1)
	go func() {
		_, err := p.Usage(context.Background())
		first <- err
	}()
	deadline := time.Now().Add(20 * time.Second)
	for started, _ := runs(); started == 0; started, _ = runs() {
		if time.Now().After(deadline) {
			t.Fatal("no probe started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := p.Usage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if started, _ := runs(); started != 1 {
		t.Errorf("%d claudes started", started)
	}
}

// A session's reading newer than the probe's stands; an older one gives way.
func TestTheNewestReadingWins(t *testing.T) {
	p, _ := usageProbe(t)
	session := core.ProviderUsage{Provider: "claude", Sampled: time.Now().Add(time.Hour), Windows: []core.UsageWindow{{Name: "Current session", Used: .5}}}
	remember(session)
	u, err := p.Usage(context.Background())
	if err != nil || len(u.Windows) != 1 || u.Windows[0].Used != .5 {
		t.Fatalf("with a newer session reading: %+v, %v", u, err)
	}
	resetUsage()
	session.Sampled = time.Now().Add(-time.Hour)
	remember(session)
	u, err = p.Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checkProbed(t, u)
}

// Signed out, or on an API key, there's no plan to read: no claude is started.
func TestUsageSignedOutStartsNothing(t *testing.T) {
	p, runs := usageProbe(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	u, err := p.Usage(context.Background())
	if started, _ := runs(); err != nil || len(u.Windows) != 0 || started != 0 {
		t.Errorf("signed out: %+v, %v; %d claudes started", u, err, started)
	}
}
