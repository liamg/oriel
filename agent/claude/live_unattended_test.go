package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
)

// TestLiveRefuseNeverPrompts runs the real claude as a utility agent runs it: an Oriel tool still runs, a tool the
// spec allows needs nobody, and a command and a claude.ai connector (the user's Claude Docs) are refused without a
// permission or a dialog, so the turn ends with nobody answering anything. ORIEL_LIVE=1 runs it, as the user, with the user's own sign-in: so not under isolate, which
// hides it. ORIEL_LIVE_POLICY runs it on another policy, to compare.
func TestLiveRefuseNeverPrompts(t *testing.T) {
	if os.Getenv("ORIEL_LIVE") != "1" {
		t.Skip("ORIEL_LIVE=1 runs the real claude")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	policy := agent.Policy(os.Getenv("ORIEL_LIVE_POLICY"))
	if policy == "" {
		policy = agent.PolicyRefuse
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cwd := t.TempDir()
	echoed := make(chan string, 4)
	echo := agenttest.Echo
	echo.Call = func(ctx context.Context, in json.RawMessage) (string, error) {
		echoed <- string(in)
		return agenttest.Echo.Call(ctx, in)
	}
	s, err := (&Provider{OrielBin: exe}).Start(ctx, agent.StartSpec{Cwd: cwd, StateDir: t.TempDir(), Model: "haiku", Policy: policy,
		Unattended: true, Allow: []string{"WebSearch"}, Tools: []agent.Tool{echo}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var evs []agent.Event
	defer func() { t.Logf("the screen ended: %s", s.(*session).lastScreen()) }()
	// What bounds a wait is go test's own -timeout: nothing here measures a while.
	wait := func(what string, match func(agent.Event) bool) {
		t.Helper()
		for ev := range s.Events() {
			evs = append(evs, ev)
			if match(ev) {
				return
			}
		}
		t.Fatalf("waiting for %s: its events ended; saw %+v", what, evs)
	}
	wait("ready", func(ev agent.Event) bool { _, ok := ev.(agent.Ready); return ok })
	if err := s.Send(ctx, agent.Input{Text: "Do these three steps in order, each with its tool, without asking me anything: " +
		"1. call the mcp__oriel__echo tool with text \"probe\"; " +
		"2. use the Bash tool to run exactly: touch probe.txt; " +
		"3. use WebSearch to search for \"golang\"; " +
		"4. call the tool mcp__claude_ai_Claude_Docs__guide with items [\"topic.index\"]. " +
		"Then reply with one line per step saying what each tool returned."}); err != nil {
		t.Fatal(err)
	}
	wait("the turn to end", func(ev agent.Event) bool { _, ok := ev.(agent.TurnEnd); return ok })
	ran := map[string]string{}
	for _, ev := range evs {
		switch ev := ev.(type) {
		case agent.Permission:
			t.Errorf("a permission was asked: %+v", ev)
		case agent.Dialog:
			if ev.Shown {
				t.Errorf("a dialog opened: %+v", ev)
			}
		case agent.Message:
			if !ev.Partial && ev.Role == core.ItemAssistant {
				t.Logf("said: %s", ev.Text)
			}
		case agent.ToolCall:
			ran[ev.ID] = ev.Tool
		case agent.ToolResult:
			t.Logf("%s: error %v: %.200s", ran[ev.ID], ev.IsError, ev.Output)
			if ran[ev.ID] == "mcp__oriel__echo" && (ev.IsError || !strings.Contains(ev.Output, "echo: probe")) {
				t.Errorf("the Oriel tool didn't run: %q", ev.Output)
			}
			if ran[ev.ID] == "mcp__claude_ai_Claude_Docs__guide" && (!ev.IsError || !strings.Contains(ev.Output, "nobody is here")) {
				t.Errorf("the connector outside Allow wasn't refused by Oriel: %q", ev.Output)
			}
			if ran[ev.ID] == "WebSearch" && ev.IsError {
				t.Errorf("the allowed WebSearch was refused: %q", ev.Output)
			}
		}
	}
	if len(ran) == 0 {
		t.Error("no tool calls came from its transcript")
	}
	select {
	case in := <-echoed:
		t.Logf("the Oriel tool ran with %s", in)
	default:
		t.Error("the Oriel tool never ran")
	}
	if _, err := os.Stat(filepath.Join(cwd, "probe.txt")); err == nil {
		t.Error("the command ran")
	}
}
