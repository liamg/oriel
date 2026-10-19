package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/testrun"
	"github.com/liamg/oriel/ui/widget"
)

const roundGoSrc = `package pricing

import "strings"

// RoundHalfEven rounds a decimal string to a whole number, ties to even; negative ties are wrong.
func RoundHalfEven(s string) string {
	switch s {
	case "2.5":
		return "2"
	case "-3.5":
		return "-3"
	}
	return strings.TrimSuffix(s, ".0")
}

func RoundMinor(v int) int { return v }
`

const roundTestSrc = `package pricing

import (
	"os"
	"testing"
)

func TestRoundMinor(t *testing.T) {
	if RoundMinor(5) != 5 {
		t.Fatal("bad")
	}
}

func TestRoundHalfEven(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"2.5", "2.5", "2"},
		{"-3.5", "-3.5", "-4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RoundHalfEven(c.in); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestFlaky(t *testing.T) {
	if _, err := os.Stat("FLIP"); err == nil {
		t.Fatal("flipped")
	}
}
`

// testsScene is a Go package with a passing test, a table test with a failing row, and a test that fails while a
// file outside it (flipFile) exists, open in the editor.
func testsScene(t *testing.T) (*errScene, string) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go")
	}
	dir := t.TempDir()
	flip := filepath.Join(t.TempDir(), "flip")
	flipFiles[dir] = flip
	for name, body := range map[string]string{
		"go.mod":        "module example.com/pricing\n\ngo 1.22\n",
		"round.go":      roundGoSrc,
		"round_test.go": strings.Replace(roundTestSrc, "FLIP", flip, 1),
	} {
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	s := newScene(t, "", dir, mockupSize)
	s.run("editor.open", registry.Args{"path": filepath.Join(dir, "round_test.go")})
	s.waitHighlights()
	s.frame()
	return s, dir
}

// flipFiles are each tests scene's flip file, by its directory.
var flipFiles = map[string]string{}

func (s *errScene) tests() *testsPlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*testsPlugin); ok {
			return x
		}
	}
	return nil
}

// runDone frames until the run in progress has finished.
func (s *errScene) runDone(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for s.tests().running() {
		if time.Now().After(deadline) {
			t.Fatal("the run didn't finish")
		}
		s.sh.runTodo()
		s.frame()
		time.Sleep(10 * time.Millisecond)
	}
	s.sh.runTodo()
	s.frame()
	s.frame()
}

// testDecor are the tests decorations on a 1-based line.
func (s *errScene) testDecor(line int) []registry.Decoration {
	x := s.editor()
	p := x.current()
	return x.decorations(p, 0, p.Buf().Lines())[line-1]
}

func hasDecor(ds []registry.Decoration, kind registry.DecorationKind, text string) bool {
	return slices.ContainsFunc(ds, func(d registry.Decoration) bool { return d.Kind == kind && strings.Contains(d.Text, text) })
}

// TR-1: ▶ in the gutter on every test, subtest and table row, each running it when clicked.
func TestTestGutter(t *testing.T) {
	s, _ := testsScene(t)
	for _, line := range []int{8, 14, 16, 17, 28} {
		ds := s.testDecor(line)
		i := slices.IndexFunc(ds, func(d registry.Decoration) bool { return d.Kind == registry.Sign && d.Text == "▶" })
		if i < 0 {
			t.Errorf("no ▶ on line %d: %+v", line, ds)
			continue
		}
		if ds[i].Command != "test.run" || ds[i].Args["key"] == "" {
			t.Errorf("the ▶ on line %d runs %q %v", line, ds[i].Command, ds[i].Args)
		}
	}
	if ds := s.testDecor(9); slices.ContainsFunc(ds, func(d registry.Decoration) bool { return d.Text == "▶" }) {
		t.Error("a ▶ on a line with no test")
	}
	s.frame()
	s.wantSignBesideNumber(t, "14", "▶")
}

// wantSignBesideNumber checks the sign drawn on the row numbered num starts after the number ends.
func (s *errScene) wantSignBesideNumber(t *testing.T, num, sign string) {
	t.Helper()
	n, g := s.signOnRow(t, num, sign)
	if end := n.At.X + n.Run.Advance; g.At.X < end {
		t.Errorf("the sign at x=%.1f covers the number, which ends at x=%.1f", g.At.X, end)
	}
}

// signOnRow is the line number num drawn in the gutter and the sign drawn on its row.
func (s *errScene) signOnRow(t *testing.T, num, sign string) (n, g *render.Op) {
	t.Helper()
	for i, op := range s.list.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == num {
			n = &s.list.Ops[i]
		}
		if op.Kind == render.OpGlyphs && op.Run.Text == sign && n != nil && op.At.Y > n.At.Y-8 && op.At.Y < n.At.Y+8 {
			return n, &s.list.Ops[i]
		}
	}
	t.Fatalf("no %q and %q on one row", num, sign)
	return nil, nil
}

// TR-1: ␣tt runs the nearest test; it shows ✓ and its duration on its line, and the status line counts it.
func TestRunNearest(t *testing.T) {
	s, _ := testsScene(t)
	s.keys("10G")
	if k, _ := s.reg.Lookup([]string{"n"}, []string{" ", "t", "t"}); k == nil || k.Command != "test.run.nearest" {
		t.Fatalf("␣tt is %+v", k)
	}
	s.run("test.run.nearest", nil)
	s.runDone(t)
	if ds := s.testDecor(8); !hasDecor(ds, registry.VirtualText, "✓") {
		t.Errorf("no ✓ on TestRoundMinor's line: %+v", ds)
	}
	r, _ := s.tests().history.Last(testrun.ByUser)
	if r == nil || len(r.Scope) != 1 || !strings.HasSuffix(r.Scope[0], "TestRoundMinor") {
		t.Fatalf("the run's scope: %+v", r)
	}
	s.wantText(t, "the status line", "TESTS", "1 ✓")
}

// TR-2: "a failing fixture test renders the block at the assertion line": want and got side by side, the output,
// and its four actions; the failing row says what it got.
func TestFailureBlock(t *testing.T) {
	s, dir := testsScene(t)
	s.run("test.run.file", nil)
	s.runDone(t)
	if ds := s.testDecor(17); !hasDecor(ds, registry.VirtualText, "got -3") {
		t.Errorf("the failing row doesn't say what it got: %+v", ds)
	}
	if ds := s.testDecor(14); !hasDecor(ds, registry.VirtualText, "1 of 2 failed") {
		t.Errorf("the table test's line: %+v", ds)
	}
	ds := s.testDecor(22)
	if !slices.ContainsFunc(ds, func(d registry.Decoration) bool { return d.Kind == registry.Block }) {
		t.Fatalf("no failure block at the assertion, line 22: %+v", ds)
	}
	s.keys("20Gzt")
	s.frame()
	s.wantText(t, "the failure block", "TestRoundHalfEven/-3.5", "WANT", "GOT", "-4", "-3", "OUTPUT", "got -3, want -4",
		"jump to assertion", "debug this case", "ask agent to explain", "ask agent to fix")
	if a := s.access("ask agent to explain"); a == nil || !a.Disabled {
		t.Errorf("explain is on with no agent to ask: %+v", a)
	}

	// With something to explain it, explain shows its answer under the failure and changes no file.
	before := snapshotDir(t, dir)
	asked := ""
	s.reg.Contribute(registry.Explainer{Name: "test", Explain: func(_ context.Context, r registry.ExplainRequest) (string, error) {
		asked = r.Subject + "\n" + r.Context
		return "**RoundHalfEven** rounds -3.5 toward zero.", nil
	}})
	s.frame()
	s.frame()
	s.click(centre(s.access("ask agent to explain").Bounds))
	s.explainer().wait()
	s.sh.runTodo()
	s.frame()
	s.frame()
	s.wantText(t, "the explain card", "RoundHalfEven", "rounds -3.5 toward zero.") // the bold its own run
	if !strings.Contains(asked, "TestRoundHalfEven/-3.5") || !strings.Contains(asked, "want -4") {
		t.Errorf("explain was asked about %q", asked)
	}
	if after := snapshotDir(t, dir); after != before {
		t.Error("explaining changed a file")
	}

	// Jump to assertion puts the cursor on it.
	s.keys("gg")
	s.click(centre(s.access("jump to assertion").Bounds))
	if got := s.editor().e.Cursor().Line; got != 21 {
		t.Errorf("jump to assertion went to line %d, want 22", got+1)
	}
}

func snapshotDir(t *testing.T, dir string) string {
	var b strings.Builder
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			body, _ := os.ReadFile(p)
			st, _ := d.Info()
			b.WriteString(p + string(body) + st.ModTime().String())
		}
		return nil
	})
	return b.String()
}

// TR-3: the Tests panel: a tree by package, test and case with each one's state; ✕ narrows it to failures; a case's
// output; and a test that failed then passed with nothing changed is flaky.
func TestTestsPanel(t *testing.T) {
	s, dir := testsScene(t)
	os.WriteFile(flipFiles[dir], nil, 0o644)
	s.run("test.run.file", nil)
	s.runDone(t)
	os.Remove(flipFiles[dir])
	s.run("test.run.last", nil)
	s.runDone(t)
	s.run("view.open", registry.Args{"id": TestsView, "place": "dock", "edge": "bottom", "size": 320})
	s.frame()
	s.frame()
	s.wantText(t, "the tests panel", "example.com/pricing", "TestRoundMinor", "TestRoundHalfEven", "-3.5", "TestFlaky", "flaky")
	s.click(centre(s.access("Failed only").Bounds))
	s.frame()
	var rows []string
	var walk func(its []widget.TreeItem)
	walk = func(its []widget.TreeItem) {
		for _, it := range its {
			rows = append(rows, it.Label)
			walk(it.Children)
		}
	}
	known, _ := s.tests().known()
	walk(s.tests().treeItems(known, s.tests().latest(), s.tests().panel.filter))
	if got := strings.Join(rows, " "); got != "example.com/pricing TestRoundHalfEven -3.5" {
		t.Errorf("failed-only rows: %q", got)
	}
	s.clickText(t, "-3.5")
	s.frame()
	s.wantText(t, "the case's output", "got -3, want -4")
	r, _ := s.tests().history.Last(testrun.ByUser)
	if !s.tests().history.Flaky(testrun.Key(r.Tests[0].Package, "TestFlaky")) {
		t.Error("TestFlaky isn't flaky")
	}
}

// The Tests panel lists every test in the workspace before any has run, and drops a result whose test file is gone.
func TestTestsPanelFindsTests(t *testing.T) {
	s, dir := testsScene(t)
	other := filepath.Join(dir, "other", "other_test.go")
	os.MkdirAll(filepath.Dir(other), 0o755)
	os.WriteFile(other, []byte("package other\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) { t.Fatal(\"no\") }\n"), 0o644)
	s.tests().rescan(other)
	rows := func() string {
		known, _ := s.tests().known()
		var out []string
		var walk func(its []widget.TreeItem)
		walk = func(its []widget.TreeItem) {
			for _, it := range its {
				out = append(out, it.Label+it.Glyph)
				walk(it.Children)
			}
		}
		walk(s.tests().treeItems(known, s.tests().latest(), 0))
		return strings.Join(out, " ")
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(rows(), "TestRoundMinor") && time.Now().Before(deadline) {
		s.sh.runTodo()
		time.Sleep(10 * time.Millisecond)
	}
	if got := rows(); got != "example.com/pricing TestFlaky○ TestRoundHalfEven○ -3.5○ 2.5○ TestRoundMinor○ example.com/pricing/other TestOther○" {
		t.Fatalf("rows before a run: %q", got)
	}
	s.run("test.run.all", nil)
	s.runDone(t)
	if got := rows(); !strings.Contains(got, "TestOther✕") {
		t.Fatalf("rows after the run: %q", got)
	}
	os.Remove(other)
	s.tests().rescan(other)
	if got := rows(); strings.Contains(got, "other") {
		t.Errorf("a deleted file's test is still listed: %q", got)
	}
}

// TR-4: "a save re-runs only the affected packages in the Go fixture": with watch on, saving a.go runs a, b and c
// (which import it), not d.
func TestWatchRunsAffected(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go")
	}
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-r", filepath.Join("..", "testrun", "testdata", "go", "watch")+"/.", dir).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	s := newScene(t, "", dir, mockupSize)
	s.run("editor.open", registry.Args{"path": filepath.Join(dir, "a", "a.go")})
	s.frame()
	s.run("test.watch", nil)
	if !s.tests().watch {
		t.Fatal("watch didn't turn on")
	}
	s.keys(":w<CR>")
	deadline := time.Now().Add(30 * time.Second)
	for !s.tests().running() && s.tests().history.Runs() == nil && time.Now().Before(deadline) {
		s.sh.runTodo()
		time.Sleep(10 * time.Millisecond)
	}
	s.runDone(t)
	r, ok := s.tests().history.Last(testrun.ByWatch)
	if !ok {
		t.Fatal("no watch run")
	}
	var pkgs []string
	for _, k := range r.Scope {
		pkg, _, _ := strings.Cut(k, "\x00")
		pkgs = append(pkgs, pkg)
	}
	slices.Sort(pkgs)
	if got := strings.Join(slices.Compact(pkgs), " "); got != "example.com/watch/a example.com/watch/b example.com/watch/c" {
		t.Errorf("the watch run's packages: %s", got)
	}
}

// TR-4: a coverage run marks covered and uncovered lines in the gutter.
func TestCoverageGutter(t *testing.T) {
	s, dir := testsScene(t)
	s.run("test.coverage", nil)
	s.runDone(t)
	s.run("editor.open", registry.Args{"path": filepath.Join(dir, "round.go")})
	s.frame()
	edge := func(line int) string {
		for _, d := range s.testDecor(line) {
			if d.Kind == registry.LineBg && strings.HasPrefix(d.Edge, "tests.coverage") {
				return d.Edge
			}
		}
		return ""
	}
	if got := edge(9); got != "tests.coverage.covered" {
		t.Errorf("line 9 (case \"2.5\" returns) is %q", got)
	}
	if got := edge(13); got != "tests.coverage.uncovered" {
		t.Errorf("line 13 (never reached) is %q", got)
	}
	s.run("test.coverage.toggle", nil)
	if edge(9) != "" {
		t.Error("toggling coverage off left the gutter marked")
	}
	s.run("test.coverage.toggle", nil)
	s.reg.SetCoverage("other", filepath.Join(dir, "round.go"), map[int]int{1: 0}) // line 2
	if got := edge(2); got != "tests.coverage.uncovered" {
		t.Errorf("another tool's coverage isn't in the gutter: %q", got)
	}
}
