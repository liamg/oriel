package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent/agenttest"
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
func (s *errScene) toolAnswer(t *testing.T, ch <-chan toolResult) toolResult {
	t.Helper()
	var r toolResult
	got := false
	s.until(t, "the tool's answer", func() bool {
		select {
		case r = <-ch:
			got = true
		default:
		}
		return got
	})
	return r
}

func (s *errScene) agentDebug(t *testing.T, a *agenttest.FakeSession, name string, args map[string]any) toolResult {
	t.Helper()
	return s.toolAnswer(t, callAgentTool(a, name, args))
}

// DB-3 done when: "the scope is enforced: eval allowed, step denied when not granted". The agent of a work session
// reads the paused stack and a frame and evaluates (allowed), is refused stepping and continuing (off), sets a
// breakpoint once the user allows it on a permission card (ask), and steps once the user grants it in the panel.
func TestAgentDebuggerTools(t *testing.T) {
	lw := newLocalHost(t)
	s, main, finish := debugSceneOn(t, "testdata/dap/agent.jsonl", lw.connect(t))
	x := s.debugger()
	waitState(t, s.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.run("session.start", registry.Args{"kind": "work", "name": "w", "provider": "fake", "repos": lw.repo})
	var a *agenttest.FakeSession
	s.until(t, "the agent", func() bool {
		select {
		case a = <-lw.fake.Started:
		default:
		}
		return a != nil
	})
	for _, name := range []string{"debug_stack", "debug_frame", "debug_variables", "debug_eval", "debug_breakpoint", "debug_continue", "debug_step"} {
		if !slices.Contains(toolNames(a), name) {
			t.Errorf("the agent wasn't offered %s", name)
		}
	}

	s.run("debug.breakpoint.set", registry.Args{"file": main, "line": 19})
	s.keyCommand(t, "<F5>")
	s.pausedAt(t, "main.main", 18)
	s.wantText(t, "the access the user grants", "AGENT'S DEBUGGER ACCESS", "READ FRAMES", "EVAL", "SET BREAKPOINTS · ASK", "STEP/CONTINUE · OFF")
	s.lint("the Debug panel with the agent's access")

	// Reading the stack and a frame is allowed.
	// ref: dap/specification.md#Requests_StackTrace
	// quote: "The request returns a stacktrace from the current execution state of a given thread."
	r := s.agentDebug(t, a, "debug_stack", nil)
	if r.err != nil || !strings.Contains(r.out, "#0 main.main at main.go:19  (selected)") || !strings.Contains(r.out, "breakpoints:\n  main.go:19") {
		t.Fatalf("debug_stack = %q, %v", r.out, r.err)
	}
	r = s.agentDebug(t, a, "debug_frame", nil)
	if r.err != nil || !strings.Contains(r.out, "total int = 0") || !strings.Contains(r.out, "i int = 1") {
		t.Fatalf("debug_frame = %q, %v", r.out, r.err)
	}

	// Eval is allowed, and shows in the debug console as the agent's.
	// ref: dap/specification.md#Requests_Evaluate
	// quote: "Evaluates the given expression in the context of a stack frame."
	r = s.agentDebug(t, a, "debug_eval", map[string]any{"expression": "i * 10"})
	if r.err != nil || !strings.HasSuffix(r.out, "10") {
		t.Fatalf("debug_eval = %q, %v", r.out, r.err)
	}
	s.until(t, "the agent's eval in the console", func() bool { return s.consoleHas("10") && x.console[len(x.console)-1].Kind == "result" })
	s.wantText(t, "the agent's eval in the console", "agent › i * 10")

	// Step and continue are off: refused, and the debuggee doesn't move.
	gen := x.sess.gen
	for _, call := range []struct {
		name string
		args map[string]any
	}{{"debug_step", map[string]any{"kind": "into"}}, {"debug_continue", nil}} {
		r = s.agentDebug(t, a, call.name, call.args)
		if r.err == nil || !strings.Contains(r.err.Error(), "debug.agent.step is off") {
			t.Errorf("%s while off = %q, %v", call.name, r.out, r.err)
		}
	}
	if x.sess.state != debugPaused || x.sess.gen != gen {
		t.Fatalf("a refused step moved the debuggee: %s", x.sess.state)
	}

	// Setting a breakpoint asks: a permission card on the session, which the user allows.
	ch := callAgentTool(a, "debug_breakpoint", map[string]any{"file": "main.go", "line": 12, "condition": "v == 2"})
	var ask core.Ask
	s.until(t, "the permission card", func() bool {
		se, ok := s.store.State().Session("w")
		if !ok || len(se.Snapshot().Asks) == 0 {
			return false
		}
		ask = se.Snapshot().Asks[0]
		return true
	})
	if ask.Kind != core.AskPermission || ask.Tool != "mcp__oriel__debug_breakpoint" || ask.Summary != "set a breakpoint at main.go:12 (cond v == 2)" {
		t.Fatalf("ask = %+v", ask)
	}
	if len(x.bps[main]) != 1 {
		t.Fatalf("the breakpoint was set before the user said yes: %+v", x.bps[main])
	}
	s.run("ask.answer", registry.Args{"id": "w", "decision": "allow"})
	if r = s.toolAnswer(t, ch); r.err != nil || r.out != "set a breakpoint at main.go:12" {
		t.Fatalf("debug_breakpoint = %q, %v", r.out, r.err)
	}
	if bs := x.bps[main]; len(bs) != 2 || bs[0].Line != 11 || bs[0].Condition != "v == 2" {
		t.Fatalf("breakpoints = %+v", bs)
	}

	// The user grants stepping in the panel; the agent steps and continues to the breakpoints.
	s.click(s.lastTextAt(t, "STEP/CONTINUE · OFF"))
	if l := x.level(debugStep); l != "allow" {
		t.Fatalf("the chip set step to %q", l)
	}
	s.wantText(t, "step granted", "STEP/CONTINUE")
	// ref: dap/specification.md#Requests_StepIn
	// quote: "The request resumes the given thread to step into a function/method and allows all other threads to run freely by resuming them."
	r = s.agentDebug(t, a, "debug_step", map[string]any{"kind": "into"})
	if r.err != nil || r.out != "paused (step) in main.half at main.go:9" {
		t.Fatalf("debug_step = %q, %v", r.out, r.err)
	}
	// ref: dap/specification.md#Types_SourceBreakpoint
	// quote: "The expression for conditional breakpoints."
	r = s.agentDebug(t, a, "debug_continue", nil)
	if r.err != nil || r.out != "paused (breakpoint) in main.main at main.go:19" {
		t.Fatalf("debug_continue = %q, %v", r.out, r.err)
	}
	r = s.agentDebug(t, a, "debug_continue", nil)
	if r.err != nil || r.out != "paused (breakpoint) in main.half at main.go:12" {
		t.Fatalf("debug_continue to the conditional breakpoint = %q, %v", r.out, r.err)
	}
	s.pausedAt(t, "main.half", 11)

	// Eval turned off is refused too.
	s.run("debug.agent.access", registry.Args{"action": "eval", "level": "off"})
	if r = s.agentDebug(t, a, "debug_eval", map[string]any{"expression": "v"}); r.err == nil || !strings.Contains(r.err.Error(), "debug.agent.eval is off") {
		t.Errorf("eval while off = %q, %v", r.out, r.err)
	}

	s.keyCommand(t, "<S-F5>")
	s.debugUntil(t, "the end", func() bool { return x.sess.state == debugEnded })
	x.wait()
	finish()
}
