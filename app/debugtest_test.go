package app

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// pricingScene is a Go package with a table test whose -3.5 row fails, open at its test file, delve answering from
// transcript.
func pricingScene(t *testing.T, transcript string) (*errScene, string, func()) {
	t.Helper()
	s, root, finish := debugFixture(t, filepath.Join("testdata", "dap", "pricing"), "dlv", transcript, "round_test.go", nil)
	s.waitHighlights()
	return s, root, finish
}

// inlineValue is the inline values chip on a 1-based line, "" when it has none.
func (s *errScene) inlineValue(line int) string {
	for _, d := range s.testDecor(line) {
		if d.Kind == registry.VirtualText && d.Bg != "" {
			return d.Text
		}
	}
	return ""
}

// clickSign clicks the sign on the row numbered num, with mods held.
func (s *errScene) clickSign(t *testing.T, num, sign string, mods render.Mods) {
	t.Helper()
	_, g := s.signOnRow(t, num, sign)
	s.clickWith(render.Point{X: g.At.X + g.Run.Advance/2, Y: g.At.Y - g.Run.Ascent/2}, mods)
}

// DB-2: ⇧ on a table row's ▶ debugs just that row; paused, the frame's variables show after the lines that name
// them (08-debug), and follow the frame selected.
func TestDebugTestCase(t *testing.T) {
	s, root, finish := pricingScene(t, "testdata/dap/go-test.jsonl")
	x := s.debugger()
	round := filepath.Join(root, "round.go")
	s.run("debug.breakpoint.set", registry.Args{"file": round, "line": 9})

	// ref: go/help-testflag.txt
	// quote: "Each of these flags is also recognized with an optional 'test.' prefix,"
	s.clickSign(t, "14", "▶", render.ModShift)
	s.pausedAt(t, "pricing.roundHalfEven", 8)
	if args := x.sess.cfg.Args; !slices.Equal(args, []string{"-test.v", "-test.run", `^TestRoundHalfEven$/^-2\.5$`}) || x.sess.cfg.Extra["mode"] != "test" {
		t.Errorf("launched %+v, want the -2.5 row alone", x.sess.cfg)
	}
	if runs := s.tests().history.Runs(); len(runs) > 0 || s.tests().running() {
		t.Errorf("debugging ran the tests too: %+v", runs)
	}

	// ref: dap/specification.md#Types_Variable
	// quote: "The variable's value."
	s.waitHighlights()
	s.debugUntil(t, "inline values", func() bool { return s.inlineValue(8) != "" })
	for line, want := range map[int]string{6: "v = -2.5  p = 0", 7: "scaled = -2.5", 8: "floor = -2"} {
		if got := s.inlineValue(line); got != want {
			t.Errorf("round.go:%d shows %q, want %q", line, got, want)
		}
	}
	for _, line := range []int{1, 5, 10, 12} {
		if got := s.inlineValue(line); got != "" {
			t.Errorf("round.go:%d shows %q, past the frame's lines", line, got)
		}
	}
	s.wantText(t, "the values beside the code", "v = -2.5  p = 0", "scaled = -2.5", "floor = -2")
	for i, l := range x.console { // the build's line names the temporary directory
		x.console[i].Text = strings.ReplaceAll(l.Text, root, "/src/pricing")
	}
	s.golden("debug")

	// The caller's frame: the test's closure, its values on its own lines.
	s.click(s.textAt(t, "pricing.TestRoundHalfEven.func1"))
	s.pausedAt(t, "pricing.TestRoundHalfEven.func1", 18)
	s.debugUntil(t, "the closure's values", func() bool { return s.inlineValue(18) != "" })
	if got := s.inlineValue(18); !strings.Contains(got, "t = ") {
		t.Errorf("round_test.go:18 shows %q", got)
	}
	if got := s.inlineValue(17); got != "" {
		t.Errorf("round_test.go:17, outside the closure, shows %q", got)
	}

	// Turned off, they go.
	if err := s.reg.Options().Set(InlineValuesOption, registry.LayerUser, registry.At{}, false); err != nil {
		t.Fatal(err)
	}
	if got := s.inlineValue(18); got != "" {
		t.Errorf("with inline values off, line 18 shows %q", got)
	}

	s.keyCommand(t, "<S-F5>")
	s.debugUntil(t, "the end", func() bool { return x.sess.state == debugEnded })
	x.wait()
	if got := s.inlineValue(18); got != "" {
		t.Errorf("after the session, line 18 shows %q", got)
	}
	finish()
}

// DB-2: the failure block's "debug this case" debugs the failing row alone, stopping at its assertion.
func TestDebugFailingCase(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go")
	}
	s, root, finish := pricingScene(t, "testdata/dap/go-test-failing.jsonl")
	x := s.debugger()
	s.run("test.run.file", nil)
	s.runDone(t)
	s.keys("18Gzt")
	s.frame()
	s.click(centre(s.access("debug this case").Bounds))
	test := filepath.Join(root, "round_test.go")
	if b := x.bps[test]; len(b) != 1 || b[0].Line != 19 {
		t.Fatalf("breakpoints %+v, want one at the assertion, line 20", b)
	}
	s.pausedAt(t, "pricing.TestRoundHalfEven.func1", 19)
	if args := x.sess.cfg.Args; !slices.Contains(args, `^TestRoundHalfEven$/^-3\.5$`) {
		t.Errorf("launched with %q, want the -3.5 row alone", args)
	}
	s.debugUntil(t, "got's value", func() bool { return strings.Contains(s.inlineValue(19), "got = -3") })
	s.keyCommand(t, "<S-F5>")
	s.debugUntil(t, "the end", func() bool { return x.sess.state == debugEnded })
	x.wait()
	finish()
}

// DB-2: a pytest case debugs the same way: ⇧ on its parametrize id's ▶ runs pytest under debugpy on that case alone.
func TestDebugPytestCase(t *testing.T) {
	s, root, finish := debugFixture(t, filepath.Join("testdata", "dap", "pypricing"), "debugpy", "testdata/dap/py-test.jsonl", "test_pricing.py", nil)
	s.waitHighlights()
	x := s.debugger()
	s.run("debug.breakpoint.set", registry.Args{"file": filepath.Join(root, "pricing.py"), "line": 8})

	// ref: pytest/doc/en/explanation/goodpractices.rst
	// quote: "command line arguments can be used in any combination of directories, file names or node ids."
	s.clickSign(t, "9", "▶", render.ModShift)
	s.pausedAt(t, "round_half_even", 7)
	if args := x.sess.cfg.Args; !slices.Equal(args, []string{filepath.Join(root, "test_pricing.py") + "::test_round_half_even[-2.5]"}) ||
		x.sess.cfg.Extra["module"] != "pytest" {
		t.Errorf("launched %+v, want the -2.5 case alone", x.sess.cfg)
	}
	s.waitHighlights()
	s.debugUntil(t, "inline values", func() bool { return s.inlineValue(7) != "" })
	for line, want := range map[int]string{4: "v = -2.5  p = 0", 6: "scaled = -2.5", 7: "floor = -2"} {
		if got := s.inlineValue(line); got != want {
			t.Errorf("pricing.py:%d shows %q, want %q", line, got, want)
		}
	}
	s.keyCommand(t, "<S-F5>")
	s.debugUntil(t, "the end", func() bool { return x.sess.state == debugEnded })
	x.wait()
	finish()
}
