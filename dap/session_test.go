package dap_test

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/dap"
	"github.com/liamg/oriel/dap/daptest"
)

// Re-record with the adapters installed: go test ./dap -run TestLive -record
var record = flag.Bool("record", false, "run TestLive against the real adapters and rewrite testdata/transcripts")

// fixture is a small program and the adapter that debugs it. Lines are 0-based.
type fixture struct {
	name    string // testdata/<name>, transcripts/<name>.jsonl
	adapter string // a built-in spec's name
	main    string
	version []string // the command whose output is the transcript's version

	entry   int // the breakpoint in the loop that calls half
	hit     int // the first line of half, with a hit condition
	cond    int // the conditional breakpoint
	logLine int // the logpoint

	// What the adapter does, from the recording (ref: oracle).
	stepIn  int    // where stepping into half lands
	next    int    // where a step over from there lands
	frame   string // the innermost frame's name at the conditional breakpoint
	caller  string // its caller's
	locals  string // the first scope's name
	fields  [2]string
	pointOf string // the type the point's value names
}

var fixtures = []fixture{
	// ref: oracle dlv 1.27.2
	{name: "go", adapter: "dlv", main: "main.go", version: []string{"dlv", "version"},
		entry: 18, hit: 9, cond: 11, logLine: 12,
		stepIn: 8, next: 9, frame: "main.half", caller: "main.main", locals: "Locals", fields: [2]string{"X", "Y"}, pointOf: "main.point"},
	// ref: oracle debugpy 1.8.22
	{name: "py", adapter: "debugpy", main: "main.py", version: []string{"python3", "-c", "import debugpy, sys; print('debugpy', debugpy.__version__, 'python', sys.version.split()[0])"},
		entry: 16, hit: 7, cond: 9, logLine: 10,
		stepIn: 7, next: 8, frame: "half", caller: "main", locals: "Locals", fields: [2]string{"x", "y"}, pointOf: "Point"},
}

func (fx fixture) spec(t *testing.T) dap.AdapterSpec {
	for _, s := range dap.Builtins() {
		if s.Name == fx.adapter {
			return s
		}
	}
	t.Fatalf("no built-in adapter %s", fx.adapter)
	return dap.AdapterSpec{}
}

// project copies a fixture to a temporary directory and returns its real path.
func project(t *testing.T, name string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("testdata", name)
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644)
	}
	return dir
}

// result is what the script saw.
type result struct {
	caps        dap.Capabilities
	first       []dap.BreakpointState
	firstStop   dap.Stopped
	firstStack  []dap.Frame
	threads     []dap.Thread
	stepIn      stop
	next        stop
	stepOut     stop
	reset       []dap.BreakpointState
	hitStop     stop
	hitV        dap.Variable
	condStop    stop
	condOutput  string
	scopes      []dap.Scope
	locals      []dap.Variable
	children    []dap.Variable
	watch       dap.Variable
	repl        dap.Variable
	callerI     dap.Variable
	bad         error
	afterNext   stop
	out         dap.Variable
	output      string
	exited      *dap.Exited
	terminated  bool
	stopErr     error
	unsupported []string
}

type stop struct {
	ev    dap.Stopped
	stack []dap.Frame
}

// events collects what the adapter says, for the script to wait on.
type events struct {
	ch     chan dap.Event
	output strings.Builder
	exited *dap.Exited
	ended  bool
}

func (e *events) next(t *testing.T) dap.Event {
	t.Helper()
	select {
	case ev := <-e.ch:
		switch ev := ev.(type) {
		case dap.Output:
			if ev.Category != "telemetry" {
				e.output.WriteString(ev.Text)
			}
		case dap.Exited:
			e.exited = &ev
		case dap.Terminated:
			e.ended = true
		}
		return ev
	case <-time.After(60 * time.Second):
		t.Fatal("no event from the adapter in 60s")
		return nil
	}
}

func (e *events) stopped(t *testing.T) dap.Stopped {
	t.Helper()
	for {
		if s, ok := e.next(t).(dap.Stopped); ok {
			return s
		}
		if e.ended {
			t.Fatalf("the session ended waiting for a stop; output %q", e.output.String())
		}
	}
}

// script debugs a fixture: breakpoints (a plain one, one with a hit condition, a conditional one and a logpoint),
// steps in, over and out, the stack, scopes and variables, watch and REPL evaluation in two frames, then runs to
// the end.
func script(t *testing.T, fx fixture, launch dap.Launcher, root string) *result {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	main := filepath.Join(root, fx.main)
	ev := &events{ch: make(chan dap.Event, 1024)}
	r := &result{}
	cfg, ok := fx.spec(t).Infer(main)
	if !ok {
		t.Fatalf("%s infers no config for %s", fx.adapter, main)
	}
	c, err := dap.Start(ctx, fx.spec(t), cfg, dap.Options{Launch: launch, OnEvent: func(e dap.Event) { ev.ch <- e },
		Breakpoints: map[string][]dap.Breakpoint{main: {{Line: fx.entry}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		select {
		case <-c.Done():
		default:
			c.Stop(context.Background())
		}
	}()
	r.caps = c.Capabilities()
	r.first = c.Breakpoints(main)

	stopAt := func() stop {
		t.Helper()
		s := ev.stopped(t)
		frames, err := c.Stack(ctx, s.Thread)
		if err != nil {
			t.Fatal(err)
		}
		return stop{s, frames}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	first := stopAt()
	r.firstStop, r.firstStack = first.ev, first.stack
	th := first.ev.Thread
	r.threads, err = c.Threads(ctx)
	must(err)

	must(c.Step(ctx, th, dap.StepIn))
	r.stepIn = stopAt()
	must(c.Step(ctx, th, dap.StepOver))
	r.next = stopAt()
	must(c.Step(ctx, th, dap.StepOut))
	r.stepOut = stopAt()

	r.reset, err = c.SetBreakpoints(ctx, main, []dap.Breakpoint{
		{Line: fx.hit, HitCondition: "== 2"},
		{Line: fx.cond, Condition: "v == 3"},
		{Line: fx.logLine, LogMessage: "out is {out}"},
	})
	must(err)

	must(c.Continue(ctx, th))
	r.hitStop = stopAt()
	r.hitV, err = c.Evaluate(ctx, r.hitStop.stack[0].ID, "v", dap.ContextWatch)
	must(err)

	must(c.Continue(ctx, th))
	r.condStop = stopAt()
	r.condOutput = ev.output.String()
	top := r.condStop.stack[0].ID
	r.scopes, err = c.Scopes(ctx, top)
	must(err)
	r.locals, err = c.Variables(ctx, r.scopes[0].Ref)
	must(err)
	for _, v := range r.locals {
		if v.Name == "p" && v.Ref > 0 {
			r.children, err = c.Variables(ctx, v.Ref)
			must(err)
		}
	}
	r.watch, err = c.Evaluate(ctx, top, "scaled * 2", dap.ContextWatch)
	must(err)
	r.repl, err = c.Evaluate(ctx, top, "v + 1", dap.ContextREPL)
	must(err)
	r.callerI, err = c.Evaluate(ctx, r.condStop.stack[1].ID, "i", dap.ContextWatch)
	must(err)
	_, r.bad = c.Evaluate(ctx, top, "nosuchname", dap.ContextREPL)

	must(c.Step(ctx, th, dap.StepOver))
	r.afterNext = stopAt()
	r.out, err = c.Evaluate(ctx, r.afterNext.stack[0].ID, "out", dap.ContextWatch)
	must(err)

	_, err = c.SetBreakpoints(ctx, main, nil)
	must(err)
	must(c.Continue(ctx, th))
	for !ev.ended {
		ev.next(t)
	}
	r.output, r.exited, r.terminated = ev.output.String(), ev.exited, ev.ended
	r.stopErr = c.Stop(ctx)
	return r
}

// run gets a fixture's session: replayed from its transcript, or live (and recorded with -record).
func run(t *testing.T, fx fixture, live bool) *result {
	root := project(t, fx.name)
	path := filepath.Join("testdata", "transcripts", fx.name+".jsonl")
	v := daptest.MachineVars(root)
	if !live {
		tr, err := daptest.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		fake := daptest.NewFake(tr, v, 10*time.Second)
		r := script(t, fx, fake.Launcher(), root)
		if err := fake.Wait(); err != nil {
			t.Fatalf("replay: %v", err)
		}
		return r
	}
	spec := fx.spec(t)
	if !dap.Installed(spec) {
		t.Skipf("%s isn't installed", spec.Name)
	}
	if !*record {
		return script(t, fx, dap.Exec, root)
	}
	rec := &daptest.Recorder{Vars: v}
	r := script(t, fx, rec.Launcher(dap.Exec), root)
	out, err := exec.Command(fx.version[0], fx.version[1:]...).Output()
	if err != nil {
		t.Fatal(err)
	}
	version := strings.Join(strings.Fields(strings.SplitN(strings.TrimSpace(string(out)), "\nBuild", 2)[0]), " ")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := rec.Transcript(daptest.Header{Adapter: spec.Name, Version: version, Cmd: spec.Cmd}).Write(f); err != nil {
		t.Fatal(err)
	}
	return r
}

// TestSession replays each adapter's recorded session and checks what the debugger UI would show.
func TestSession(t *testing.T) {
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) { check(t, fx, run(t, fx, false)) })
	}
}

// TestLive runs the same session against each adapter that's installed (dlv, and python3 with debugpy); -record
// rewrites the transcripts.
func TestLive(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) { check(t, fx, run(t, fx, true)) })
	}
}
