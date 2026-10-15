package dap_test

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/dap"
)

// check holds a session's result to what the debugger UI needs from it.
func check(t *testing.T, fx fixture, r *result) {
	if r == nil {
		return
	}
	main := ""
	if len(r.first) > 0 {
		main = r.first[0].Path
	}
	if filepath.Base(main) != fx.main {
		t.Fatalf("first breakpoints = %+v, want one in %s", r.first, fx.main)
	}

	// ref: dap/specification.md#Types_Capabilities
	// quote: "The debug adapter supports conditional breakpoints."
	c := r.caps
	if !c.SupportsConfigurationDoneRequest || !c.SupportsConditionalBreakpoints || !c.SupportsHitConditionalBreakpoints || !c.SupportsLogPoints {
		t.Errorf("capabilities = %+v, want configurationDone, conditional, hit conditional and log points", c)
	}

	// ref: dap/specification.md#Requests_SetBreakpoints
	// quote: "When a breakpoint is hit, a `stopped` event (with reason `breakpoint`) is generated."
	if len(r.first) != 1 || !r.first[0].Verified || r.first[0].Line != fx.entry {
		t.Errorf("first breakpoints = %+v, want one verified on line %d", r.first, fx.entry)
	}
	if r.firstStop.Reason != "breakpoint" || !slices.Contains(r.firstStop.Breakpoints, r.first[0].ID) {
		t.Errorf("first stop = %+v, want breakpoint %d", r.firstStop, r.first[0].ID)
	}
	// ref: dap/specification.md#Requests_StackTrace
	// quote: "The request returns a stacktrace from the current execution state of a given thread."
	wantFrame(t, "first stop", r.firstStack, fx.caller, main, fx.entry)

	// ref: dap/specification.md#Requests_Threads
	// quote: "The request retrieves a list of all threads."
	if !slices.ContainsFunc(r.threads, func(th dap.Thread) bool { return th.ID == r.firstStop.Thread }) {
		t.Errorf("threads %+v lack the stopped thread %d", r.threads, r.firstStop.Thread)
	}

	// ref: dap/specification.md#Requests_StepIn
	// quote: "The request resumes the given thread to step into a function/method and allows all other threads to run freely by resuming them."
	wantStep(t, "step in", r.stepIn, fx.frame, main, fx.stepIn)
	if len(r.stepIn.stack) < 2 || r.stepIn.stack[1].Name != fx.caller {
		t.Errorf("step in: stack %+v, want %s under %s", r.stepIn.stack, fx.frame, fx.caller)
	}
	// ref: dap/specification.md#Requests_Next
	// quote: "The request executes one step (in the given granularity) for the specified thread and allows all other threads to run freely by resuming them."
	wantStep(t, "next", r.next, fx.frame, main, fx.next)
	// ref: dap/specification.md#Requests_StepOut
	// quote: "The request resumes the given thread to step out (return) from a function/method and allows all other threads to run freely by resuming them."
	wantStep(t, "step out", r.stepOut, fx.caller, main, fx.entry)

	// ref: dap/specification.md#Requests_SetBreakpoints
	// quote: "Sets multiple breakpoints for a single source and clears all previous breakpoints in that source."
	var lines []int
	for _, b := range r.reset {
		if b.Verified {
			lines = append(lines, b.Line)
		}
	}
	if !slices.Equal(lines, []int{fx.hit, fx.cond, fx.logLine}) {
		t.Fatalf("breakpoints set again = %+v, want verified on %d, %d, %d", r.reset, fx.hit, fx.cond, fx.logLine)
	}
	if slices.Contains(r.hitStop.ev.Breakpoints, r.first[0].ID) {
		t.Errorf("the first breakpoint was hit after it was cleared")
	}

	// ref: dap/specification.md#Types_SourceBreakpoint
	// quote: "The expression that controls how many hits of the breakpoint are ignored."
	if r.hitStop.ev.Reason != "breakpoint" || !slices.Contains(r.hitStop.ev.Breakpoints, r.reset[0].ID) || r.hitV.Value != "3" {
		t.Errorf("hit condition == 2 stopped %+v with v = %q, want its second hit, v = 3", r.hitStop.ev, r.hitV.Value)
	}
	wantFrame(t, "hit condition", r.hitStop.stack, fx.frame, main, fx.hit)

	// ref: dap/specification.md#Types_SourceBreakpoint
	// quote: "The expression for conditional breakpoints."
	if r.condStop.ev.Reason != "breakpoint" || !slices.Contains(r.condStop.ev.Breakpoints, r.reset[1].ID) {
		t.Errorf("conditional stop = %+v, want breakpoint %d", r.condStop.ev, r.reset[1].ID)
	}
	wantFrame(t, "conditional", r.condStop.stack, fx.frame, main, fx.cond)
	// ref: dap/specification.md#Types_SourceBreakpoint
	// quote: "but log the message instead. Expressions within `{}` are interpolated."
	if !strings.Contains(r.condOutput, "out is 10\n") || strings.Contains(r.condOutput, "{out}") || strings.Contains(r.condOutput, "out is 15") {
		t.Errorf("output by the conditional stop = %q, want the logpoint's out is 10 (i = 2) and no more", r.condOutput)
	}

	// ref: dap/specification.md#Requests_Scopes
	// quote: "The request returns the variable scopes for a given stack frame ID."
	if len(r.scopes) == 0 || r.scopes[0].Name != fx.locals {
		t.Fatalf("scopes = %+v, want %s first", r.scopes, fx.locals)
	}
	byName := func(vs []dap.Variable) map[string]dap.Variable {
		m := map[string]dap.Variable{}
		for _, v := range vs {
			m[v.Name] = v
		}
		return m
	}
	locals := byName(r.locals)
	if locals["v"].Value != "3" || locals["scaled"].Value != "30" {
		t.Errorf("locals = %+v, want v = 3 and scaled = 30", r.locals)
	}
	// ref: dap/specification.md#Types_Variable
	// quote: "If the value is structured (has children), a handle is provided to retrieve the children with the `variables` request."
	if p := locals["p"]; p.Ref == 0 || p.Type != fx.pointOf {
		t.Errorf("p = %+v, want a %s with children", p, fx.pointOf)
	}
	kids := byName(r.children)
	if kids[fx.fields[0]].Value != "3" || kids[fx.fields[1]].Value != "30" || kids[fx.fields[0]].Evaluate != "p."+fx.fields[0] {
		t.Errorf("p's children = %+v, want %s = 3 and %s = 30", r.children, fx.fields[0], fx.fields[1])
	}

	// ref: dap/specification.md#Requests_Evaluate
	// quote: "Evaluates the given expression in the context of a stack frame."
	if r.watch.Value != "60" || r.repl.Value != "4" || r.callerI.Value != "3" {
		t.Errorf("watch scaled * 2 = %q, repl v + 1 = %q, i in the caller's frame = %q; want 60, 4, 3", r.watch.Value, r.repl.Value, r.callerI.Value)
	}
	var de *dap.Error
	if !errors.As(r.bad, &de) || de.Command != "evaluate" || de.Message == "" {
		t.Errorf("evaluating an unknown name = %v, want the adapter's error", r.bad)
	}
	wantStep(t, "next to the logpoint", r.afterNext, fx.frame, main, fx.logLine)
	if r.out.Value != "15" {
		t.Errorf("out = %q, want 15", r.out.Value)
	}

	// ref: dap/specification.md#Events_Exited
	// quote: "The event indicates that the debuggee has exited and returns its exit code."
	if r.exited == nil || r.exited.Code != 0 || !strings.Contains(r.output, "out is 15\n") || !strings.Contains(r.output, "total 50\n") {
		t.Errorf("end: exited %+v, output %q; want exit code 0 and the program's output", r.exited, r.output)
	}
	// ref: dap/specification.md#Events_Terminated
	// quote: "The event indicates that debugging of the debuggee has terminated."
	if !r.terminated || r.stopErr != nil {
		t.Errorf("terminated %v, stop %v", r.terminated, r.stopErr)
	}
}

func wantFrame(t *testing.T, what string, stack []dap.Frame, name, path string, line int) {
	t.Helper()
	if len(stack) == 0 || stack[0].Name != name || stack[0].Path != path || stack[0].Line != line {
		t.Errorf("%s: stack %+v, want %s at %s:%d first", what, stack, name, filepath.Base(path), line)
	}
}

func wantStep(t *testing.T, what string, s stop, name, path string, line int) {
	t.Helper()
	if s.ev.Reason != "step" {
		t.Errorf("%s: stopped for %q, want step", what, s.ev.Reason)
	}
	wantFrame(t, what, s.stack, name, path, line)
}
