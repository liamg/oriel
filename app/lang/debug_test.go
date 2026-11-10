package lang_test

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/dap"
	"github.com/liamg/oriel/dap/daptest"
	"github.com/liamg/oriel/registry"
	reglang "github.com/liamg/oriel/registry/lang"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
)

// Re-record with dlv (and debugpy with pytest) installed: go test ./app/lang -run 'TestDebug|TestAgentDebuggerTools' -dap-record
var dapRecord = flag.Bool("dap-record", false, "run the debugger's tests against dlv and rewrite their recordings in testdata/dap")

const debugTranscript = "testdata/dap/go.jsonl"

// adapterVersions are the versions the recordings were made with.
var adapterVersions = map[string]string{"dlv": "Delve Debugger Version: 1.27.2", "debugpy": "debugpy 1.8.22 python 3.11.2"}

// debugScene is the dap module's Go fixture open in the editor, delve answering from the recorded session (or live,
// recording it, with -dap-record).
func debugScene(t *testing.T) (*apptest.Scene, string, func()) {
	return debugSceneWith(t, nil)
}

func debugSceneWith(t *testing.T, change func(*apptest.Config)) (*apptest.Scene, string, func()) {
	t.Helper()
	return debugSceneOn(t, debugTranscript, change)
}

// debugSceneOn is debugSceneWith on the recording at transcript, which -dap-record rewrites.
func debugSceneOn(t *testing.T, transcript string, change func(*apptest.Config)) (*apptest.Scene, string, func()) {
	t.Helper()
	s, root, finish := debugFixture(t, filepath.Join("..", "..", "dap", "testdata", "go"), "dlv", transcript, "main.go", change)
	return s, filepath.Join(root, "main.go"), finish
}

// debugFixture copies the project in src to a directory and opens a file of it, the adapter (a built-in's name)
// answering from the transcript; finish checks every request had an answer (or with -dap-record, writes the
// transcript).
func debugFixture(t *testing.T, src, adapter, transcript, open string, change func(*apptest.Config)) (*apptest.Scene, string, func()) {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "project") // named the same each run: the launch's name is the folder's
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(src)
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join(src, e.Name()))
		os.WriteFile(filepath.Join(root, e.Name()), b, 0o644)
	}
	s := apptest.New(t, apptest.Options{WorkDir: root, Config: change})
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
		s.Registry().Contribute(reglang.DebugAdapter(spec))
	}
	s.Run("editor.open", registry.Args{"path": filepath.Join(root, open)})
	s.Frame()
	return s, root, finish
}

// debugUntil frames until ok, failing after a while.
func debugUntil(t *testing.T, s *apptest.Scene, what string, ok func() bool) {
	t.Helper()
	if !s.Headless.Until(60*time.Second, ok) { // a debugger starts slowly
		t.Fatalf("waited for %s; the console has %v\n%s", what, debugOf(s).Console(), s.Text())
	}
	s.Quiet()
}

// pausedAt waits until the debugger is paused with its frames and variables in, at a frame named name on a 0-based
// line.
func pausedAt(t *testing.T, s *apptest.Scene, name string, line int) {
	t.Helper()
	x := debugOf(s)
	debugUntil(t, s, name, func() bool { return x.PausedAt(name, line) })
}

// lastTextAt is the middle of the last run of text reading text: in a panel drawn after the editor.
func lastTextAt(t *testing.T, s *apptest.Scene, text string) render.Point {
	t.Helper()
	p, ok := s.TextAt(text)
	if !ok {
		t.Fatalf("no %q in the frame:\n%s", text, s.Text())
	}
	return p
}

func consoleHas(s *apptest.Scene, text string) bool {
	return slices.ContainsFunc(debugOf(s).Console(), func(l lang.ConsoleLine) bool { return strings.Contains(l.Text, text) })
}

// textAt is the middle of the first run of text that reads text.
func textAt(s *apptest.Scene, text string) render.Point { return s.TextPoint(text) }

// A Go session in the Debug panel, driven by the gutter, keys and the panel: breakpoints (a plain one set by
// clicking a line number, a conditional one and a logpoint), stepping, the stack, variables, a watch and the REPL.
func TestDebugSession(t *testing.T) {
	s, main, finish := debugScene(t)
	x := debugOf(s)

	s.Click(textAt(s, "19"), 0)
	if got := x.Breakpoints(main); len(got) != 1 || got[0].Line != 18 {
		t.Fatalf("clicking line 19's number set %+v, want a breakpoint there", got)
	}
	if ds := testDecor(s, 19); !hasDecor(ds, uireg.Sign, "●") {
		t.Errorf("line 19 has no breakpoint sign: %+v", ds)
	}
	s.EditorKeys("12G")
	s.EditorKeys(":Breakpoint v == 3<CR>")
	s.EditorKeys("13G")
	s.EditorKeys(":Logpoint out is {out}<CR>")
	s.EditorKeys(":Watch scaled * 2<CR>")
	if ds := testDecor(s, 12); !hasDecor(ds, uireg.Sign, "○") || !hasDecor(ds, uireg.VirtualText, "cond v == 3") {
		t.Errorf("line 12 lacks the conditional breakpoint: %+v", ds)
	}
	if ds := testDecor(s, 13); !hasDecor(ds, uireg.Sign, "◆") {
		t.Errorf("line 13 lacks the logpoint: %+v", ds)
	}

	// ref: dap/overview.md#Launch Sequencing
	// quote: "After the response to `configurationDone` is sent, the debug adapter may respond to the `launch` or `attach` request, and then the debug session has started."
	s.Press("<F5>")
	pausedAt(t, s, "main.main", 18)
	if !s.Showing(lang.DebugView) {
		t.Error("the Debug panel didn't open")
	}
	if ds := testDecor(s, 19); !hasDecor(ds, uireg.Sign, "➜") || !hasDecor(ds, uireg.LineBg, "") {
		t.Errorf("the current line isn't marked: %+v", ds)
	}
	wantText(t, s, "paused at the first breakpoint", "PAUSED · BREAKPOINT", "main.main", "main.go:19", "paused main.go:19",
		"3 breakpoints · 1 conditional · 1 logpoint", "hit 1")

	// ref: dap/specification.md#Requests_StepIn
	// quote: "The request resumes the given thread to step into a function/method and allows all other threads to run freely by resuming them."
	s.Press("<F11>")
	pausedAt(t, s, "main.half", 8)
	s.Press("<F10>")
	pausedAt(t, s, "main.half", 9)
	s.Press("<S-F11>")
	pausedAt(t, s, "main.main", 18)

	s.Click(textAt(s, "19"), 0)
	if len(x.Breakpoints(main)) != 2 {
		t.Fatalf("clicking line 19 again left %+v", x.Breakpoints(main))
	}
	// ref: dap/specification.md#Types_SourceBreakpoint
	// quote: "The expression for conditional breakpoints."
	s.Press("<F5>")
	pausedAt(t, s, "main.half", 11)
	if !consoleHas(s, "out is 10") || consoleHas(s, "{out}") {
		t.Errorf("the logpoint's output isn't in the console: %v", x.Console())
	}
	debugUntil(t, s, "the watch", func() bool { return x.WatchValues() == 1 })
	wantText(t, s, "at the conditional breakpoint", "PAUSED · BREAKPOINT", "LOCALS", "v", "int 3", "scaled", "int 30", "scaled * 2", "60")

	// ref: dap/specification.md#Types_Variable
	// quote: "If the value is structured (has children), a handle is provided to retrieve the children with the `variables` request."
	s.Click(lastTextAt(t, s, "p"), 0)
	s.Press("l")
	debugUntil(t, s, "p's fields", func() bool { return x.HasVariable("p.X") })
	wantText(t, s, "p opened", "X", "Y")

	// ref: dap/specification.md#Requests_Evaluate
	// quote: "Evaluates the given expression in the context of a stack frame."
	s.Click(textAt(s, "› evaluate in the selected frame"), 0)
	s.Type(strings.Split("v + 1", "")...)
	s.Type("CR")
	debugUntil(t, s, "the REPL's answer", func() bool {
		c := x.Console()
		return consoleHas(s, "4") && len(c) > 0 && c[len(c)-1].Kind == "result"
	})
	if c := x.Console(); c[len(c)-1].Text != "4" {
		t.Errorf("v + 1 = %q, want 4", c[len(c)-1].Text)
	}

	// The caller's frame: its variables and line.
	s.Click(textAt(s, "main.main"), 0)
	pausedAt(t, s, "main.main", 18)
	wantText(t, s, "in main's frame", "i", "int 3", "total")

	// ref: dap/specification.md#Requests_Disconnect
	// quote: "The `disconnect` request asks the debug adapter to disconnect from the debuggee (thus ending the debug session) and then to shut down itself (the debug adapter)."
	s.Press("<S-F5>")
	debugUntil(t, s, "the end", x.Ended)
	x.Wait()
	if strings.Contains(s.Text(), "paused main.go") {
		t.Error("the status line still says paused")
	}
	finish()
}

// Breakpoints are kept for the directory, and a config in .oriel/debug.toml is what F5 starts.
func TestDebugConfigsAndState(t *testing.T) {
	state := t.TempDir()
	s, main, _ := debugSceneWith(t, func(c *apptest.Config) { c.StateDir = state })
	x := debugOf(s)
	s.Run("debug.breakpoint.set", registry.Args{"file": main, "line": 10, "hit": "== 2"})
	s.Run("Watch", registry.Args{"args": "v"})
	if x.Counts() != "1 breakpoint · 1 conditional" {
		t.Errorf("counts = %q", x.Counts())
	}
	x.Reload()
	if b := x.Breakpoints(main); len(b) != 1 || b[0].HitCondition != "== 2" || len(x.Watches()) != 1 {
		t.Errorf("reloaded %+v, %q", b, x.Watches())
	}

	dir := filepath.Dir(main)
	os.MkdirAll(filepath.Join(dir, ".oriel"), 0o755)
	os.WriteFile(filepath.Join(dir, ".oriel", "debug.toml"), []byte("[[debug]]\nname = \"tests\"\ntype = \"go\"\nprogram = \".\"\nmode = \"test\"\n\n"+
		"[[debug]]\nname = \"attach\"\ntype = \"go\"\nrequest = \"attach\"\npid = 42\n"), 0o644)
	cfgs, err := x.Configs()
	if err != nil || len(cfgs) != 2 || cfgs[0].Program != dir || cfgs[0].Extra["mode"] != "test" || cfgs[1].PID != 42 {
		t.Fatalf("configs = %+v, %v", cfgs, err)
	}
	if err := x.StartNamed("nope"); err == nil || !strings.Contains(err.Error(), "no launch config") {
		t.Errorf("an unknown config: %v", err)
	}
	s.Registry().Contribute(reglang.DebugAdapter{Name: "dlv", Type: "go", Cmd: []string{"oriel-no-such-dlv"}, Source: "test"})
	if err := x.StartNamed(""); err == nil || !strings.Contains(err.Error(), "isn't installed") && !strings.Contains(err.Error(), "is installed") {
		t.Errorf("without dlv: %v", err)
	}
}
