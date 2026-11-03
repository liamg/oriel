package lang_test

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
)

// pricingScene is a Go package with a table test whose -3.5 row fails, open at its test file, delve answering from
// transcript.
func pricingScene(t *testing.T, transcript string) (*apptest.Scene, string, func()) {
	t.Helper()
	s, root, finish := debugFixture(t, filepath.Join("testdata", "dap", "pricing"), "dlv", transcript, "round_test.go", nil)
	s.WaitSyntax()
	return s, root, finish
}

// inlineValue is the inline values chip on a 1-based line, "" when it has none.
func inlineValue(s *apptest.Scene, line int) string {
	for _, d := range testDecor(s, line) {
		if d.Kind == uireg.VirtualText && d.Bg != "" {
			return d.Text
		}
	}
	return ""
}

// ⇧ on a table row's ▶ debugs just that row; paused, the frame's variables show after the lines that name
// them, and follow the frame selected.
func TestDebugTestCase(t *testing.T) {
	s, root, finish := pricingScene(t, "testdata/dap/go-test.jsonl")
	x := debugOf(s)
	round := filepath.Join(root, "round.go")
	s.Run("debug.breakpoint.set", registry.Args{"file": round, "line": 9})

	// ref: go/help-testflag.txt
	// quote: "Each of these flags is also recognized with an optional 'test.' prefix,"
	s.Click(s.SignPoint("14", "▶"), render.ModShift)
	pausedAt(t, s, "pricing.roundHalfEven", 8)
	if cfg := x.Launched(); !slices.Equal(cfg.Args, []string{"-test.v", "-test.run", `^TestRoundHalfEven$/^-2\.5$`}) || cfg.Extra["mode"] != "test" {
		t.Errorf("launched %+v, want the -2.5 row alone", cfg)
	}
	if runs := testsOf(s).History().Runs(); len(runs) > 0 || testsOf(s).Running() {
		t.Errorf("debugging ran the tests too: %+v", runs)
	}

	// ref: dap/specification.md#Types_Variable
	// quote: "The variable's value."
	s.WaitSyntax()
	debugUntil(t, s, "inline values", func() bool { return inlineValue(s, 8) != "" })
	for line, want := range map[int]string{6: "v = -2.5  p = 0", 7: "scaled = -2.5", 8: "floor = -2"} {
		if got := inlineValue(s, line); got != want {
			t.Errorf("round.go:%d shows %q, want %q", line, got, want)
		}
	}
	for _, line := range []int{1, 5, 10, 12} {
		if got := inlineValue(s, line); got != "" {
			t.Errorf("round.go:%d shows %q, past the frame's lines", line, got)
		}
	}
	wantText(t, s, "the values beside the code", "v = -2.5  p = 0", "scaled = -2.5", "floor = -2")
	x.ReplaceInConsole(root, "/src/pricing") // the build's line names the temporary directory
	s.Golden("debug")

	// The caller's frame: the test's closure, its values on its own lines.
	s.Click(textAt(s, "pricing.TestRoundHalfEven.func1"), 0)
	pausedAt(t, s, "pricing.TestRoundHalfEven.func1", 18)
	debugUntil(t, s, "the closure's values", func() bool { return inlineValue(s, 18) != "" })
	if got := inlineValue(s, 18); !strings.Contains(got, "t = ") {
		t.Errorf("round_test.go:18 shows %q", got)
	}
	if got := inlineValue(s, 17); got != "" {
		t.Errorf("round_test.go:17, outside the closure, shows %q", got)
	}

	// Turned off, they go.
	if err := s.Registry().Options().Set(lang.InlineValuesOption, registry.LayerUser, registry.At{}, false); err != nil {
		t.Fatal(err)
	}
	if got := inlineValue(s, 18); got != "" {
		t.Errorf("with inline values off, line 18 shows %q", got)
	}

	s.Press("<S-F5>")
	debugUntil(t, s, "the end", x.Ended)
	x.Wait()
	if got := inlineValue(s, 18); got != "" {
		t.Errorf("after the session, line 18 shows %q", got)
	}
	finish()
}

// The failure block's "debug this case" debugs the failing row alone, stopping at its assertion.
func TestDebugFailingCase(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go")
	}
	s, root, finish := pricingScene(t, "testdata/dap/go-test-failing.jsonl")
	x := debugOf(s)
	s.Run("test.run.file", nil)
	runDone(t, s)
	s.EditorKeys("18Gzt")
	s.Frame()
	s.ClickOn("debug this case")
	test := filepath.Join(root, "round_test.go")
	if b := x.Breakpoints(test); len(b) != 1 || b[0].Line != 19 {
		t.Fatalf("breakpoints %+v, want one at the assertion, line 20", b)
	}
	pausedAt(t, s, "pricing.TestRoundHalfEven.func1", 19)
	if args := x.Launched().Args; !slices.Contains(args, `^TestRoundHalfEven$/^-3\.5$`) {
		t.Errorf("launched with %q, want the -3.5 row alone", args)
	}
	debugUntil(t, s, "got's value", func() bool { return strings.Contains(inlineValue(s, 19), "got = -3") })
	s.Press("<S-F5>")
	debugUntil(t, s, "the end", x.Ended)
	x.Wait()
	finish()
}

// A pytest case debugs the same way: ⇧ on its parametrize id's ▶ runs pytest under debugpy on that case alone.
func TestDebugPytestCase(t *testing.T) {
	s, root, finish := debugFixture(t, filepath.Join("testdata", "dap", "pypricing"), "debugpy", "testdata/dap/py-test.jsonl", "test_pricing.py", nil)
	s.WaitSyntax()
	x := debugOf(s)
	s.Run("debug.breakpoint.set", registry.Args{"file": filepath.Join(root, "pricing.py"), "line": 8})

	// ref: pytest/doc/en/explanation/goodpractices.rst
	// quote: "command line arguments can be used in any combination of directories, file names or node ids."
	s.Click(s.SignPoint("9", "▶"), render.ModShift)
	pausedAt(t, s, "round_half_even", 7)
	if cfg := x.Launched(); !slices.Equal(cfg.Args, []string{filepath.Join(root, "test_pricing.py") + "::test_round_half_even[-2.5]"}) ||
		cfg.Extra["module"] != "pytest" {
		t.Errorf("launched %+v, want the -2.5 case alone", cfg)
	}
	s.WaitSyntax()
	debugUntil(t, s, "inline values", func() bool { return inlineValue(s, 7) != "" })
	for line, want := range map[int]string{4: "v = -2.5  p = 0", 6: "scaled = -2.5", 7: "floor = -2"} {
		if got := inlineValue(s, line); got != want {
			t.Errorf("pricing.py:%d shows %q, want %q", line, got, want)
		}
	}
	s.Press("<S-F5>")
	debugUntil(t, s, "the end", x.Ended)
	x.Wait()
	finish()
}

// Right-click ▶ → "Debug test" launches debug.test with that test's key and file, as ⇧-click does.
func TestDebugFromSignMenu(t *testing.T) {
	s, root, finish := pricingScene(t, "testdata/dap/go-test.jsonl")
	x := debugOf(s)
	s.Run("debug.breakpoint.set", registry.Args{"file": filepath.Join(root, "round.go"), "line": 9})
	s.RightClick(s.SignPoint("14", "▶"))
	s.ClickOn("Debug test")
	pausedAt(t, s, "pricing.roundHalfEven", 8)
	if cfg := x.Launched(); !slices.Equal(cfg.Args, []string{"-test.v", "-test.run", `^TestRoundHalfEven$/^-2\.5$`}) || cfg.Extra["mode"] != "test" {
		t.Errorf("launched %+v, want the -2.5 row alone", cfg)
	}
	if runs := testsOf(s).History().Runs(); len(runs) > 0 || testsOf(s).Running() {
		t.Errorf("debugging ran the tests too: %+v", runs)
	}
	s.Press("<S-F5>")
	debugUntil(t, s, "the end", x.Ended)
	x.Wait()
	finish()
}
