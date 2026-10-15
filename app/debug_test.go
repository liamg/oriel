package app

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/dap"
	"github.com/liamg/oriel/dap/daptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// Re-record with dlv (and debugpy with pytest) installed: go test ./app -run 'TestDebug|TestAgentDebuggerTools' -dap-record
var dapRecord = flag.Bool("dap-record", false, "run the debugger's tests against dlv and rewrite their recordings in testdata/dap")

const debugTranscript = "testdata/dap/go.jsonl"

// adapterVersions are the versions the recordings were made with.
var adapterVersions = map[string]string{"dlv": "Delve Debugger Version: 1.27.2", "debugpy": "debugpy 1.8.22 python 3.11.2"}

// debugScene is the dap module's Go fixture open in the editor, delve answering from the recorded session (or live,
// recording it, with -dap-record).
func debugScene(t *testing.T) (*errScene, string, func()) {
	return debugSceneWith(t, nil)
}

func debugSceneWith(t *testing.T, change func(*Config)) (*errScene, string, func()) {
	t.Helper()
	return debugSceneOn(t, debugTranscript, change)
}

// debugSceneOn is debugSceneWith on the recording at transcript, which -dap-record rewrites.
func debugSceneOn(t *testing.T, transcript string, change func(*Config)) (*errScene, string, func()) {
	t.Helper()
	s, root, finish := debugFixture(t, filepath.Join("..", "dap", "testdata", "go"), "dlv", transcript, "main.go", change)
	return s, filepath.Join(root, "main.go"), finish
}

// debugFixture copies the project in src to a directory and opens a file of it, the adapter (a built-in's name)
// answering from the transcript; finish checks every request had an answer (or with -dap-record, writes the
// transcript).
func debugFixture(t *testing.T, src, adapter, transcript, open string, change func(*Config)) (*errScene, string, func()) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(src)
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join(src, e.Name()))
		os.WriteFile(filepath.Join(root, e.Name()), b, 0o644)
	}
	s := newSceneWith(t, "", root, mockupSize, change)
	vars := daptest.MachineVars(root)
	finish := func() {}
	for _, spec := range dap.Builtins() {
		if spec.Name != adapter {
			continue
		}
		spec.Source = "test"
		if *dapRecord {
			if !dap.Installed(spec) {
				t.Skipf("%s isn't installed", adapter)
			}
			rec := &daptest.Recorder{Vars: vars}
			spec.Connect = rec.Launcher(dap.Exec)
			finish = func() {
				os.MkdirAll(filepath.Dir(transcript), 0o755)
				f, err := os.Create(transcript)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				rec.Transcript(daptest.Header{Adapter: adapter, Version: adapterVersions[adapter], Cmd: spec.Cmd}).Write(f)
			}
		} else {
			tr, err := daptest.Load(transcript)
			if err != nil {
				t.Fatal(err)
			}
			r := daptest.NewResponder(tr, vars)
			spec.Connect = r.Launcher()
			finish = func() {
				if err := r.Err(); err != nil {
					t.Errorf("requests the recording can't answer: %v", err)
				}
			}
		}
		s.reg.Contribute(registry.DebugAdapter(spec))
	}
	s.run("editor.open", registry.Args{"path": filepath.Join(root, open)})
	s.frame()
	return s, root, finish
}

func (s *errScene) debugger() *debugPlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*debugPlugin); ok {
			return x
		}
	}
	return nil
}

// debugUntil frames until ok, failing after a while.
func (s *errScene) debugUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("waited for %s; the console has %v\n%s", what, s.debugger().console, s.frameText())
		}
		s.sh.runTodo()
		s.frame()
		time.Sleep(5 * time.Millisecond)
	}
	s.sh.runTodo()
	s.frame()
	s.frame()
}

// pausedAt waits until the debugger is paused with its frames and variables in, at a frame named name on a 0-based
// line.
func (s *errScene) pausedAt(t *testing.T, name string, line int) {
	t.Helper()
	x := s.debugger()
	s.debugUntil(t, name, func() bool {
		f, ok := x.sess.top()
		return ok && f.Name == name && f.Line == line && len(x.sess.scopes) > 0 && x.sess.vars[x.sess.scopes[0].Ref] != nil
	})
}

// lastTextAt is the middle of the last run of text reading text: in a panel drawn after the editor.
func (s *errScene) lastTextAt(t *testing.T, text string) render.Point {
	t.Helper()
	for i := len(s.list.Ops) - 1; i >= 0; i-- {
		if op := s.list.Ops[i]; op.Kind == render.OpGlyphs && op.Run.Text == text {
			return render.Point{X: op.At.X + op.Run.Advance/2, Y: op.At.Y - op.Run.Ascent/2}
		}
	}
	t.Fatalf("no %q in the frame:\n%s", text, s.frameText())
	return render.Point{}
}

func (s *errScene) consoleHas(text string) bool {
	return slices.ContainsFunc(s.debugger().console, func(l consoleLine) bool { return strings.Contains(l.Text, text) })
}

// DB-1: a Go session in the Debug panel, driven by the gutter, keys and the panel: breakpoints (a plain one set by
// clicking a line number, a conditional one and a logpoint), stepping, the stack, variables, a watch and the REPL.
func TestDebugSession(t *testing.T) {
	s, main, finish := debugScene(t)
	x := s.debugger()

	s.click(s.textAt(t, "19"))
	if got := x.bps[main]; len(got) != 1 || got[0].Line != 18 {
		t.Fatalf("clicking line 19's number set %+v, want a breakpoint there", got)
	}
	if ds := s.testDecor(19); !hasDecor(ds, registry.Sign, "●") {
		t.Errorf("line 19 has no breakpoint sign: %+v", ds)
	}
	s.keys("12G")
	s.keys(":Breakpoint v == 3<CR>")
	s.keys("13G")
	s.keys(":Logpoint out is {out}<CR>")
	s.keys(":Watch scaled * 2<CR>")
	if ds := s.testDecor(12); !hasDecor(ds, registry.Sign, "○") || !hasDecor(ds, registry.VirtualText, "cond v == 3") {
		t.Errorf("line 12 lacks the conditional breakpoint: %+v", ds)
	}
	if ds := s.testDecor(13); !hasDecor(ds, registry.Sign, "◆") {
		t.Errorf("line 13 lacks the logpoint: %+v", ds)
	}

	// ref: dap/overview.md#Launch Sequencing
	// quote: "After the response to `configurationDone` is sent, the debug adapter may respond to the `launch` or `attach` request, and then the debug session has started."
	s.keyCommand(t, "<F5>")
	s.pausedAt(t, "main.main", 18)
	if !s.showing(DebugView) {
		t.Error("the Debug panel didn't open")
	}
	if ds := s.testDecor(19); !hasDecor(ds, registry.Sign, "➜") || !hasDecor(ds, registry.LineBg, "") {
		t.Errorf("the current line isn't marked: %+v", ds)
	}
	s.wantText(t, "paused at the first breakpoint", "PAUSED · BREAKPOINT", "main.main", "main.go:19", "paused main.go:19",
		"3 breakpoints · 1 conditional · 1 logpoint", "hit 1")

	// ref: dap/specification.md#Requests_StepIn
	// quote: "The request resumes the given thread to step into a function/method and allows all other threads to run freely by resuming them."
	s.keyCommand(t, "<F11>")
	s.pausedAt(t, "main.half", 8)
	s.keyCommand(t, "<F10>")
	s.pausedAt(t, "main.half", 9)
	s.keyCommand(t, "<S-F11>")
	s.pausedAt(t, "main.main", 18)

	s.click(s.textAt(t, "19"))
	if len(x.bps[main]) != 2 {
		t.Fatalf("clicking line 19 again left %+v", x.bps[main])
	}
	// ref: dap/specification.md#Types_SourceBreakpoint
	// quote: "The expression for conditional breakpoints."
	s.keyCommand(t, "<F5>")
	s.pausedAt(t, "main.half", 11)
	if !s.consoleHas("out is 10") || s.consoleHas("{out}") {
		t.Errorf("the logpoint's output isn't in the console: %v", x.console)
	}
	s.debugUntil(t, "the watch", func() bool { return len(x.panel.watchVals) == 1 })
	s.wantText(t, "at the conditional breakpoint", "PAUSED · BREAKPOINT", "LOCALS", "v", "int 3", "scaled", "int 30", "scaled * 2", "60")

	// ref: dap/specification.md#Types_Variable
	// quote: "If the value is structured (has children), a handle is provided to retrieve the children with the `variables` request."
	s.click(s.lastTextAt(t, "p"))
	s.keyCommand(t, "l")
	s.debugUntil(t, "p's fields", func() bool {
		for _, vs := range x.sess.vars {
			if slices.ContainsFunc(vs, func(v dap.Variable) bool { return v.Evaluate == "p.X" }) {
				return true
			}
		}
		return false
	})
	s.wantText(t, "p opened", "X", "Y")

	// ref: dap/specification.md#Requests_Evaluate
	// quote: "Evaluates the given expression in the context of a stack frame."
	s.click(s.textAt(t, "› evaluate in the selected frame"))
	s.typeKeys(strings.Split("v + 1", "")...)
	s.typeKeys("CR")
	s.debugUntil(t, "the REPL's answer", func() bool {
		return s.consoleHas("4") && len(x.console) > 0 && x.console[len(x.console)-1].Kind == "result"
	})
	if l := x.console[len(x.console)-1]; l.Text != "4" {
		t.Errorf("v + 1 = %q, want 4", l.Text)
	}

	// The caller's frame: its variables and line.
	s.click(s.textAt(t, "main.main"))
	s.pausedAt(t, "main.main", 18)
	s.wantText(t, "in main's frame", "i", "int 3", "total")

	// ref: dap/specification.md#Requests_Disconnect
	// quote: "The `disconnect` request asks the debug adapter to disconnect from the debuggee (thus ending the debug session) and then to shut down itself (the debug adapter)."
	s.keyCommand(t, "<S-F5>")
	s.debugUntil(t, "the end", func() bool { return x.sess.state == debugEnded })
	x.wait()
	if strings.Contains(s.frameText(), "paused main.go") {
		t.Error("the status line still says paused")
	}
	finish()
}

// DB-1: breakpoints are kept for the directory, and a config in .oriel/debug.toml is what F5 starts.
func TestDebugConfigsAndState(t *testing.T) {
	state := t.TempDir()
	s, main, _ := debugSceneWith(t, func(c *Config) { c.StateDir = state })
	x := s.debugger()
	s.run("debug.breakpoint.set", registry.Args{"file": main, "line": 10, "hit": "== 2"})
	s.run("Watch", registry.Args{"args": "v"})
	if x.counts() != "1 breakpoint · 1 conditional" {
		t.Errorf("counts = %q", x.counts())
	}
	x.bps, x.watches = nil, nil
	x.load()
	if b := x.bps[main]; len(b) != 1 || b[0].HitCondition != "== 2" || len(x.watches) != 1 {
		t.Errorf("reloaded %+v, %q", x.bps, x.watches)
	}

	dir := filepath.Dir(main)
	os.MkdirAll(filepath.Join(dir, ".oriel"), 0o755)
	os.WriteFile(filepath.Join(dir, ".oriel", "debug.toml"), []byte("[[debug]]\nname = \"tests\"\ntype = \"go\"\nprogram = \".\"\nmode = \"test\"\n\n"+
		"[[debug]]\nname = \"attach\"\ntype = \"go\"\nrequest = \"attach\"\npid = 42\n"), 0o644)
	cfgs, err := x.configs()
	if err != nil || len(cfgs) != 2 || cfgs[0].Program != dir || cfgs[0].Extra["mode"] != "test" || cfgs[1].PID != 42 {
		t.Fatalf("configs = %+v, %v", cfgs, err)
	}
	if err := x.startNamed("nope"); err == nil || !strings.Contains(err.Error(), "no launch config") {
		t.Errorf("an unknown config: %v", err)
	}
	s.reg.Contribute(registry.DebugAdapter{Name: "dlv", Type: "go", Cmd: []string{"oriel-no-such-dlv"}, Source: "test"})
	if err := x.startNamed(""); err == nil || !strings.Contains(err.Error(), "isn't installed") && !strings.Contains(err.Error(), "is installed") {
		t.Errorf("without dlv: %v", err)
	}
}
