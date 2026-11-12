package claude

import (
	"context"
	"os"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/core"
)

// TestLiveConnectorAsksOriel runs the real claude as a sandbox runs it, in bypassPermissions (on the host here, so
// the user's claude.ai connectors are there), and has it call a connector twice: each call raises Oriel's
// permission through the mod, an allow runs it, and nothing waits in claude's own terminal. ORIEL_LIVE=1 runs it, as
// the user, with the user's own sign-in and the Claude Docs connector (ORIEL_LIVE_CONNECTOR names another);
// ORIEL_LIVE_POLICY runs it on the host's gate of that policy instead.
func TestLiveConnectorAsksOriel(t *testing.T) {
	if os.Getenv("ORIEL_LIVE") != "1" {
		t.Skip("ORIEL_LIVE=1 runs the real claude")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tool := os.Getenv("ORIEL_LIVE_CONNECTOR")
	if tool == "" {
		tool = "mcp__claude_ai_Claude_Docs__guide"
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cwd := t.TempDir()
	if err := trust(nil, cwd); err != nil {
		t.Fatal(err)
	}
	spec := agent.StartSpec{Cwd: cwd, StateDir: t.TempDir(), Model: "haiku", Policy: agent.PolicyBypass,
		Sandbox: &agent.Sandbox{Name: "live"}, Exec: hostexec.Exec{}}
	if p := agent.Policy(os.Getenv("ORIEL_LIVE_POLICY")); p != "" {
		spec.Policy, spec.Sandbox = p, nil
	}
	s, err := (&Provider{OrielBin: exe}).Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defer func() { t.Logf("the screen ended: %s", s.(*session).lastScreen()) }()
	ran := map[string]string{}
	// What bounds a wait is go test's own -timeout: nothing here measures a while.
	turn := func(always bool) (asked int) {
		t.Helper()
		if err := s.Send(ctx, agent.Input{Text: "Call the tool " + tool + " once (for the guide, with items [\"topic.index\"]), " +
			"then reply in one line saying whether it succeeded. Do nothing else."}); err != nil {
			t.Fatal(err)
		}
		for ev := range s.Events() {
			switch ev := ev.(type) {
			case agent.Permission:
				asked++
				t.Logf("Oriel was asked: %s (rule %q)", ev.Tool, ev.Rule)
				if ev.Tool != tool {
					t.Errorf("asked about %s", ev.Tool)
				}
				if err := s.Answer(ctx, ev.ID, agent.Decision{Allow: true, Always: always}); err != nil {
					t.Fatal(err)
				}
			case agent.Dialog:
				if ev.Shown {
					t.Errorf("a dialog opened: %+v", ev)
				}
			case agent.ToolCall:
				ran[ev.ID] = ev.Tool
			case agent.ToolResult:
				t.Logf("%s: error %v: %.200s", ran[ev.ID], ev.IsError, ev.Output)
				if ran[ev.ID] == tool && ev.IsError {
					t.Errorf("the allowed call failed: %q", ev.Output)
				}
			case agent.Message:
				if !ev.Partial && ev.Role == core.ItemAssistant {
					t.Logf("said: %s", ev.Text)
				}
			case agent.TurnEnd:
				return asked
			}
		}
		t.Fatal("the session's events ended")
		return 0
	}
	for ev := range s.Events() {
		if _, ok := ev.(agent.Ready); ok {
			break
		}
	}
	if n := turn(true); n != 1 {
		t.Errorf("the first call asked Oriel %d times, want 1", n)
	}
	// "Always" is Oriel's rule, kept by the session host: the provider still hands it the next call.
	t.Logf("after Always, the second call asked Oriel %d times", turn(false))
}
