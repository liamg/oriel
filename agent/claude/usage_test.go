package claude

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
)

// usageProbe is the provider with the fake claude, signed in to the fixture account; runs counts the claudes started,
// and those that quit when asked.
func usageProbe(t *testing.T) (p *Provider, runs func() (started, quit int)) {
	t.Helper()
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

// A call while a probe runs waits for its reading, and starts no second claude.
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
	u, err := p.Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checkProbed(t, u)
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
	p.usage.remember(session)
	u, err := p.Usage(context.Background())
	if err != nil || len(u.Windows) != 1 || u.Windows[0].Used != .5 {
		t.Fatalf("with a newer session reading: %+v, %v", u, err)
	}
	p.usage = usageBook{}
	session.Sampled = time.Now().Add(-time.Hour)
	p.usage.remember(session)
	u, err = p.Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checkProbed(t, u)
}

// Signed out, the probe's claude shows its login screen: the probe ends there, with no reading and no error.
func TestUsageSignedOutEndsTheProbe(t *testing.T) {
	p, runs := usageProbe(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("ORIEL_FAKE_SIGNED_OUT", "1")
	start := time.Now()
	u, err := p.Usage(context.Background())
	if started, _ := runs(); err != nil || len(u.Windows) != 0 || started != 1 {
		t.Errorf("signed out: %+v, %v; %d claudes started", u, err, started)
	}
	if d := time.Since(start); d > probeTimeout/2 {
		t.Errorf("the probe took %s to see the login screen", d)
	}
}

// With claude not installed, no probe starts.
func TestUsageWithoutClaudeStartsNothing(t *testing.T) {
	p, runs := usageProbe(t)
	p.Bin = filepath.Join(t.TempDir(), "claude")
	u, err := p.Usage(context.Background())
	if started, _ := runs(); err != nil || len(u.Windows) != 0 || started != 0 {
		t.Errorf("no claude: %+v, %v; %d claudes started", u, err, started)
	}
}

// A dialog that swallows /usage ends the probe when its time is up, /usage typed once, with an error that says what
// was on screen and where claude's debug log is.
func TestUsageDialogInTheWay(t *testing.T) {
	p, _ := usageProbe(t)
	t.Setenv("ORIEL_FAKE_DIALOG", "stuck")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_, err := p.Usage(ctx)
	var pe *ProbeError
	if !errors.As(err, &pe) || !pe.Hello || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(os.Getenv("ORIEL_FAKE_RUNS")); bytes.Count(b, []byte("usage\n")) != 1 {
		t.Errorf("/usage typed %d times", bytes.Count(b, []byte("usage\n")))
	}
	if !strings.Contains(pe.Screen, "New MCP server found in .mcp.json") {
		t.Errorf("the error doesn't say what was on screen: %q", pe.Screen)
	}
	if b, rerr := os.ReadFile(pe.Log); rerr != nil || !strings.Contains(string(b), "fake claude started") || !strings.Contains(err.Error(), "claude's debug log: "+pe.Log) {
		t.Errorf("the error's debug log %q: %v", pe.Log, rerr)
	}
}

// A claude that never says hello ends the probe with an error that says it never started.
func TestUsageNoHello(t *testing.T) {
	p, _ := usageProbe(t)
	t.Setenv("ORIEL_FAKE_NO_HELLO", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := p.Usage(ctx)
	var pe *ProbeError
	if !errors.As(err, &pe) || pe.Hello || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

// $.session.usage() failing ends the probe at its time with an error that says what it failed with.
func TestUsageSaysWhySessionUsageFailed(t *testing.T) {
	p, _ := usageProbe(t)
	t.Setenv("ORIEL_FAKE_USAGE_FAILS", "Invalid time value")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := p.Usage(ctx)
	var pe *ProbeError
	if !errors.As(err, &pe) || pe.Usage != "Invalid time value" || !strings.Contains(err.Error(), "$.session.usage() failed: Invalid time value") {
		t.Fatalf("err = %v", err)
	}
}

// A call while a probe runs that fails has the probe's error, not an empty reading.
func TestUsageWhileAProbeFailsSaysWhy(t *testing.T) {
	p, runs := usageProbe(t)
	t.Setenv("ORIEL_FAKE_USAGE_FAILS", "Invalid time value")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first := make(chan error, 1)
	go func() {
		_, err := p.Usage(ctx)
		first <- err
	}()
	deadline := time.Now().Add(20 * time.Second)
	for started, _ := runs(); started == 0; started, _ = runs() {
		if time.Now().After(deadline) {
			t.Fatal("no probe started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, err := p.Usage(context.Background())
	var pe *ProbeError
	if !errors.As(err, &pe) || pe.Usage != "Invalid time value" {
		t.Errorf("the second call's err = %v", err)
	}
	<-first
	if started, _ := runs(); started != 1 {
		t.Errorf("%d claudes started", started)
	}
}

// A usage report Oriel can't decode ends the probe with an error that says so, and what the mod sent.
func TestUsageSaysWhenTheReportDidntDecode(t *testing.T) {
	p, _ := usageProbe(t)
	t.Setenv("ORIEL_FAKE_USAGE_ODD", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := p.Usage(ctx)
	var pe *ProbeError
	if !errors.As(err, &pe) || !strings.Contains(pe.Usage, "didn't decode") || !strings.Contains(pe.Usage, `"resetsAt":1760464800`) {
		t.Fatalf("err = %v", err)
	}
}
