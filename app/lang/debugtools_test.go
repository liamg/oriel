package lang_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

type toolResult struct {
	out string
	err error
}

// callAgentTool has the session's agent call a tool, as it would over MCP, without waiting.
func callAgentTool(a *agenttest.FakeSession, name string, args map[string]any) <-chan toolResult {
	done := make(chan toolResult, 1)
	go func() {
		out, err := a.CallErr(name, args)
		done <- toolResult{out, err}
	}()
	return done
}

// toolAnswer waits for a tool call's answer, running the window meanwhile.
func toolAnswer(t *testing.T, s *apptest.Scene, ch <-chan toolResult) toolResult {
	t.Helper()
	var r toolResult
	got := false
	s.Until("the tool's answer", func() bool {
		select {
		case r = <-ch:
			got = true
		default:
		}
		return got
	})
	return r
}

func agentDebug(t *testing.T, s *apptest.Scene, a *agenttest.FakeSession, name string, args map[string]any) toolResult {
	t.Helper()
	return toolAnswer(t, s, callAgentTool(a, name, args))
}

// The scope is enforced: eval allowed, step denied when not granted. The agent of a work session
// reads the paused stack and a frame and evaluates (allowed), is refused stepping and continuing (off), sets a
// breakpoint once the user allows it on a permission card (ask), and steps once the user grants it in the panel.
func TestAgentDebuggerTools(t *testing.T) {
	lw := rig.NewHost(t)
	s, main, finish := debugSceneOn(t, "testdata/dap/agent.jsonl", apptest.Connect(lw.W))
	x := debugOf(s)
	rig.WaitState(t, s.Store(), "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.Run("session.start", registry.Args{"kind": "work", "name": "w", "provider": "fake", "repos": lw.Repo})
	var a *agenttest.FakeSession
	s.Until("the agent", func() bool {
		select {
		case a = <-lw.Fake.Started:
		default:
		}
		return a != nil
	})
	for _, name := range []string{"debug_stack", "debug_frame", "debug_variables", "debug_eval", "debug_breakpoint", "debug_continue", "debug_step"} {
		if !slices.Contains(toolNames(a), name) {
			t.Errorf("the agent wasn't offered %s", name)
		}
	}

	s.Run("debug.breakpoint.set", registry.Args{"file": main, "line": 19})
	s.Press("<F5>")
	pausedAt(t, s, "main.main", 18)
	wantText(t, s, "the access the user grants", "AGENT'S DEBUGGER ACCESS", "READ FRAMES", "EVAL", "SET BREAKPOINTS · ASK", "STEP/CONTINUE · OFF")
	s.Lint("the Debug panel with the agent's access")

	// Reading the stack and a frame is allowed.
	// ref: dap/specification.md#Requests_StackTrace
	// quote: "The request returns a stacktrace from the current execution state of a given thread."
	r := agentDebug(t, s, a, "debug_stack", nil)
	if r.err != nil || !strings.Contains(r.out, "#0 main.main at main.go:19  (selected)") || !strings.Contains(r.out, "breakpoints:\n  main.go:19") {
		t.Fatalf("debug_stack = %q, %v", r.out, r.err)
	}
	r = agentDebug(t, s, a, "debug_frame", nil)
	if r.err != nil || !strings.Contains(r.out, "total int = 0") || !strings.Contains(r.out, "i int = 1") {
		t.Fatalf("debug_frame = %q, %v", r.out, r.err)
	}

	// Eval is allowed, and shows in the debug console as the agent's.
	// ref: dap/specification.md#Requests_Evaluate
	// quote: "Evaluates the given expression in the context of a stack frame."
	r = agentDebug(t, s, a, "debug_eval", map[string]any{"expression": "i * 10"})
	if r.err != nil || !strings.HasSuffix(r.out, "10") {
		t.Fatalf("debug_eval = %q, %v", r.out, r.err)
	}
	s.Until("the agent's eval in the console", func() bool { c := x.Console(); return consoleHas(s, "10") && c[len(c)-1].Kind == "result" })
	wantText(t, s, "the agent's eval in the console", "agent › i * 10")

	// Step and continue are off: refused, and the debuggee doesn't move.
	_, gen := x.Paused()
	for _, call := range []struct {
		name string
		args map[string]any
	}{{"debug_step", map[string]any{"kind": "into"}}, {"debug_continue", nil}} {
		r = agentDebug(t, s, a, call.name, call.args)
		if r.err == nil || !strings.Contains(r.err.Error(), "debug.agent.step is off") {
			t.Errorf("%s while off = %q, %v", call.name, r.out, r.err)
		}
	}
	if paused, now := x.Paused(); !paused || now != gen {
		t.Fatal("a refused step moved the debuggee")
	}

	// Setting a breakpoint asks: a permission card on the session, which the user allows.
	ch := callAgentTool(a, "debug_breakpoint", map[string]any{"file": "main.go", "line": 12, "condition": "v == 2"})
	var ask core.Ask
	s.Until("the permission card", func() bool {
		se, ok := s.Store().State().Session("w")
		if !ok || len(se.Snapshot().Asks) == 0 {
			return false
		}
		ask = se.Snapshot().Asks[0]
		return true
	})
	if ask.Kind != core.AskPermission || ask.Tool != "mcp__oriel__debug_breakpoint" || ask.Summary != "set a breakpoint at main.go:12 (cond v == 2)" {
		t.Fatalf("ask = %+v", ask)
	}
	if len(x.Breakpoints(main)) != 1 {
		t.Fatalf("the breakpoint was set before the user said yes: %+v", x.Breakpoints(main))
	}
	s.Run("ask.answer", registry.Args{"id": "w", "decision": "allow"})
	if r = toolAnswer(t, s, ch); r.err != nil || r.out != "set a breakpoint at main.go:12" {
		t.Fatalf("debug_breakpoint = %q, %v", r.out, r.err)
	}
	if bs := x.Breakpoints(main); len(bs) != 2 || bs[0].Line != 11 || bs[0].Condition != "v == 2" {
		t.Fatalf("breakpoints = %+v", bs)
	}

	// The user grants stepping in the panel; the agent steps and continues to the breakpoints.
	s.Click(lastTextAt(t, s, "STEP/CONTINUE · OFF"), 0)
	if l := x.StepLevel(); l != "allow" {
		t.Fatalf("the chip set step to %q", l)
	}
	wantText(t, s, "step granted", "STEP/CONTINUE")
	// ref: dap/specification.md#Requests_StepIn
	// quote: "The request resumes the given thread to step into a function/method and allows all other threads to run freely by resuming them."
	r = agentDebug(t, s, a, "debug_step", map[string]any{"kind": "into"})
	if r.err != nil || r.out != "paused (step) in main.half at main.go:9" {
		t.Fatalf("debug_step = %q, %v", r.out, r.err)
	}
	// ref: dap/specification.md#Types_SourceBreakpoint
	// quote: "The expression for conditional breakpoints."
	r = agentDebug(t, s, a, "debug_continue", nil)
	if r.err != nil || r.out != "paused (breakpoint) in main.main at main.go:19" {
		t.Fatalf("debug_continue = %q, %v", r.out, r.err)
	}
	r = agentDebug(t, s, a, "debug_continue", nil)
	if r.err != nil || r.out != "paused (breakpoint) in main.half at main.go:12" {
		t.Fatalf("debug_continue to the conditional breakpoint = %q, %v", r.out, r.err)
	}
	pausedAt(t, s, "main.half", 11)

	// Eval turned off is refused too.
	s.Run("debug.agent.access", registry.Args{"action": "eval", "level": "off"})
	if r = agentDebug(t, s, a, "debug_eval", map[string]any{"expression": "v"}); r.err == nil || !strings.Contains(r.err.Error(), "debug.agent.eval is off") {
		t.Errorf("eval while off = %q, %v", r.out, r.err)
	}

	s.Press("<S-F5>")
	debugUntil(t, s, "the end", x.Ended)
	x.Wait()
	finish()
}

func toolNames(a *agenttest.FakeSession) []string {
	var out []string
	for _, t := range a.Spec.Tools {
		out = append(out, t.Name)
	}
	return out
}
