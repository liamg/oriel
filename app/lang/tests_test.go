package lang_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/testrun"
	"github.com/liamg/oriel/ui/widget"
)

// testDecor are the tests decorations on a 1-based line.
func testDecor(s *apptest.Scene, line int) []uireg.Decoration {
	return s.Decorations(0, s.EditorWindow().Text.Snapshot().Lines())[line-1]
}

func hasDecor(ds []uireg.Decoration, kind uireg.DecorationKind, text string) bool {
	return slices.ContainsFunc(ds, func(d uireg.Decoration) bool { return d.Kind == kind && strings.Contains(d.Text, text) })
}

// ▶ in the gutter on every test, subtest and table row, each running it when clicked.
func TestTestGutter(t *testing.T) {
	s, _ := testsScene(t)
	for _, line := range []int{8, 14, 16, 17, 28} {
		ds := testDecor(s, line)
		i := slices.IndexFunc(ds, func(d uireg.Decoration) bool { return d.Kind == uireg.Sign && d.Text == "▶" })
		if i < 0 {
			t.Errorf("no ▶ on line %d: %+v", line, ds)
			continue
		}
		if ds[i].Command != "test.run" || ds[i].Args["key"] == "" {
			t.Errorf("the ▶ on line %d runs %q %v", line, ds[i].Command, ds[i].Args)
		}
	}
	if ds := testDecor(s, 9); slices.ContainsFunc(ds, func(d uireg.Decoration) bool { return d.Text == "▶" }) {
		t.Error("a ▶ on a line with no test")
	}
	s.Frame()
	wantSignBesideNumber(t, s, "14", "▶")
}

// wantSignBesideNumber checks the sign drawn on the row numbered num starts after the number ends.
func wantSignBesideNumber(t *testing.T, s *apptest.Scene, num, sign string) {
	t.Helper()
	n, g := signOnRow(t, s, num, sign)
	if end := n.At.X + n.Run.Advance; g.At.X < end {
		t.Errorf("the sign at x=%.1f covers the number, which ends at x=%.1f", g.At.X, end)
	}
}

// signOnRow is the line number num drawn in the gutter and the sign drawn on its row.
func signOnRow(t *testing.T, s *apptest.Scene, num, sign string) (n, g *render.Op) {
	t.Helper()
	ops := s.List().Ops
	for i, op := range ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == num {
			n = &ops[i]
		}
		if op.Kind == render.OpGlyphs && op.Run.Text == sign && n != nil && op.At.Y > n.At.Y-8 && op.At.Y < n.At.Y+8 {
			return n, &ops[i]
		}
	}
	t.Fatalf("no %q and %q on one row", num, sign)
	return nil, nil
}

// ␣tr runs the nearest test; it shows ✓ and its duration on its line, and the status line counts it.
func TestRunNearest(t *testing.T) {
	s, _ := testsScene(t)
	s.EditorKeys("10G")
	if k, _ := s.Registry().Lookup([]string{"n"}, []string{" ", "t", "r"}); k == nil || k.Command != "test.run.nearest" {
		t.Fatalf("␣tr is %+v", k)
	}
	s.Run("test.run.nearest", nil)
	runDone(t, s)
	if ds := testDecor(s, 8); !hasDecor(ds, uireg.VirtualText, "✓") {
		t.Errorf("no ✓ on TestRoundMinor's line: %+v", ds)
	}
	r, _ := testsOf(s).History().Last(testrun.ByUser)
	if r == nil || len(r.Scope) != 1 || !strings.HasSuffix(r.Scope[0], "TestRoundMinor") {
		t.Fatalf("the run's scope: %+v", r)
	}
	wantText(t, s, "the status line", "TESTS", "1 ✓")
}

// A failing fixture test renders the block at the assertion line: want and got side by side, the output,
// and its four actions; the failing row says what it got.
func TestFailureBlock(t *testing.T) {
	s, dir := testsScene(t)
	s.Run("test.run.file", nil)
	runDone(t, s)
	if ds := testDecor(s, 17); !hasDecor(ds, uireg.VirtualText, "got -3") {
		t.Errorf("the failing row doesn't say what it got: %+v", ds)
	}
	if ds := testDecor(s, 14); !hasDecor(ds, uireg.VirtualText, "1 of 2 failed") {
		t.Errorf("the table test's line: %+v", ds)
	}
	ds := testDecor(s, 22)
	if !slices.ContainsFunc(ds, func(d uireg.Decoration) bool { return d.Kind == uireg.Block }) {
		t.Fatalf("no failure block at the assertion, line 22: %+v", ds)
	}
	s.EditorKeys("20Gzt")
	s.Frame()
	wantText(t, s, "the failure block", "TestRoundHalfEven/-3.5", "WANT", "GOT", "-4", "-3", "OUTPUT", "got -3, want -4",
		"jump to assertion", "debug this case", "ask agent to explain", "ask agent to fix")
	if a := s.Find("ask agent to explain"); a == nil || !a.Disabled {
		t.Errorf("explain is on with no agent to ask: %+v", a)
	}

	// With something to explain it, explain shows its answer under the failure and changes no file.
	before := snapshotDir(t, dir)
	asked := ""
	s.Registry().Contribute(registry.Explainer{Name: "test", Explain: func(_ context.Context, r registry.ExplainRequest) (string, error) {
		asked = r.Subject + "\n" + r.Context
		return "**RoundHalfEven** rounds -3.5 toward zero.", nil
	}})
	s.Frame()
	s.Frame()
	s.ClickOn("ask agent to explain")
	s.Until("the explain card's answer", func() bool { return s.Has("rounds -3.5 toward zero.") })
	s.Frame()
	wantText(t, s, "the explain card", "RoundHalfEven", "rounds -3.5 toward zero.") // the bold its own run
	if !strings.Contains(asked, "TestRoundHalfEven/-3.5") || !strings.Contains(asked, "want -4") {
		t.Errorf("explain was asked about %q", asked)
	}
	if after := snapshotDir(t, dir); after != before {
		t.Error("explaining changed a file")
	}

	// Jump to assertion puts the cursor on it.
	s.EditorKeys("gg")
	s.ClickOn("jump to assertion")
	if got := s.EditorWindow().Cursor.Line; got != 21 {
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

// The Tests panel: a tree by package, test and case with each one's state; ✕ narrows it to failures; a case's
// output; and a test that failed then passed with nothing changed is flaky.
func TestTestsPanel(t *testing.T) {
	s, dir := testsScene(t)
	os.WriteFile(rig.FlipFile(dir), nil, 0o644)
	s.Run("test.run.file", nil)
	runDone(t, s)
	os.Remove(rig.FlipFile(dir))
	s.Run("test.run.last", nil)
	runDone(t, s)
	s.Run("view.open", registry.Args{"id": lang.TestsView, "place": "dock", "edge": "bottom", "size": 320})
	s.Frame()
	s.Frame()
	wantText(t, s, "the tests panel", "example.com/pricing", "TestRoundMinor", "TestRoundHalfEven", "-3.5", "TestFlaky", "flaky")
	s.ClickOn("Failed only")
	s.Frame()
	if got := treeRows(testsOf(s).Tree(), false); got != "example.com/pricing TestRoundHalfEven -3.5" {
		t.Errorf("failed-only rows: %q", got)
	}
	s.ClickText("-3.5")
	s.Frame()
	wantText(t, s, "the case's output", "got -3, want -4")
	r, _ := testsOf(s).History().Last(testrun.ByUser)
	if !testsOf(s).History().Flaky(testrun.Key(r.Tests[0].Package, "TestFlaky")) {
		t.Error("TestFlaky isn't flaky")
	}
}

// The Tests panel lists every test in the workspace before any has run, and drops a result whose test file is gone.
func TestTestsPanelFindsTests(t *testing.T) {
	s, dir := testsScene(t)
	other := filepath.Join(dir, "other", "other_test.go")
	os.MkdirAll(filepath.Dir(other), 0o755)
	os.WriteFile(other, []byte("package other\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) { t.Fatal(\"no\") }\n"), 0o644)
	testsOf(s).Rescan(other)
	rows := func() string { return treeRows(testsOf(s).TreeAll(), true) }
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(rows(), "TestRoundMinor") && time.Now().Before(deadline) {
		s.RunQueued()
		time.Sleep(10 * time.Millisecond)
	}
	if got := rows(); got != "example.com/pricing TestFlaky○ TestRoundHalfEven○ -3.5○ 2.5○ TestRoundMinor○ example.com/pricing/other TestOther○" {
		t.Fatalf("rows before a run: %q", got)
	}
	s.Run("test.run.all", nil)
	runDone(t, s)
	if got := rows(); !strings.Contains(got, "TestOther✕") {
		t.Fatalf("rows after the run: %q", got)
	}
	os.Remove(other)
	testsOf(s).Rescan(other)
	if got := rows(); strings.Contains(got, "other") {
		t.Errorf("a deleted file's test is still listed: %q", got)
	}
}

// A save re-runs only the affected packages in the Go fixture: with watch on, saving a.go runs a, b and c
// (which import it), not d.
func TestWatchRunsAffected(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go")
	}
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-r", filepath.Join("..", "..", "testrun", "testdata", "go", "watch")+"/.", dir).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.Run("editor.open", registry.Args{"path": filepath.Join(dir, "a", "a.go")})
	s.Frame()
	s.Run("test.watch", nil)
	if !testsOf(s).Watching() {
		t.Fatal("watch didn't turn on")
	}
	s.EditorKeys(":w<CR>")
	deadline := time.Now().Add(30 * time.Second)
	for !testsOf(s).Running() && testsOf(s).History().Runs() == nil && time.Now().Before(deadline) {
		s.RunQueued()
		time.Sleep(10 * time.Millisecond)
	}
	runDone(t, s)
	r, ok := testsOf(s).History().Last(testrun.ByWatch)
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

// A coverage run marks covered and uncovered lines in the gutter.
func TestCoverageGutter(t *testing.T) {
	s, dir := testsScene(t)
	s.Run("test.coverage", nil)
	runDone(t, s)
	s.Run("editor.open", registry.Args{"path": filepath.Join(dir, "round.go")})
	s.Frame()
	edge := func(line int) string {
		for _, d := range testDecor(s, line) {
			if d.Kind == uireg.LineBg && strings.HasPrefix(d.Edge, "tests.coverage") {
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
	s.Run("test.coverage.toggle", nil)
	if edge(9) != "" {
		t.Error("toggling coverage off left the gutter marked")
	}
	s.Run("test.coverage.toggle", nil)
	s.Registry().SetCoverage("other", filepath.Join(dir, "round.go"), map[int]int{1: 0}) // line 2
	if got := edge(2); got != "tests.coverage.uncovered" {
		t.Errorf("another tool's coverage isn't in the gutter: %q", got)
	}
}

// treeRows are the Tests panel's rows, each label with its glyph if glyphs, in order.
func treeRows(its []widget.TreeItem, glyphs bool) string {
	var out []string
	var walk func(its []widget.TreeItem)
	walk = func(its []widget.TreeItem) {
		for _, it := range its {
			if glyphs {
				out = append(out, it.Label+it.Glyph)
			} else {
				out = append(out, it.Label)
			}
			walk(it.Children)
		}
	}
	walk(its)
	return strings.Join(out, " ")
}

// signPoint is the middle of the sign drawn on the row numbered num.
func signPoint(t *testing.T, s *apptest.Scene, num, sign string) render.Point {
	t.Helper()
	_, g := signOnRow(t, s, num, sign)
	return render.Point{X: g.At.X + g.Run.Advance/2, Y: g.At.Y - g.Run.Ascent/2}
}

// Right-click ▶ opens Run test, Debug test, Run with coverage, Run file; its tooltip names them.
func TestTestSignMenu(t *testing.T) {
	s, _ := testsScene(t)
	ds := testDecor(s, 14)
	i := slices.IndexFunc(ds, func(d uireg.Decoration) bool { return d.Kind == uireg.Sign && d.Text == "▶" })
	if i < 0 || ds[i].Tooltip != "Run · ⇧ debug · right-click for more" {
		t.Fatalf("the ▶ on line 14: %+v", ds)
	}
	menu := s.RightClick(signPoint(t, s, "14", "▶"))
	for _, want := range []string{"Run test", "Debug test", "Run with coverage", "Run file"} {
		if !strings.Contains(menu, want) {
			t.Errorf("the ▶ menu lacks %q:\n%s", want, menu)
		}
	}
	s.ClickOn("Run with coverage")
	runDone(t, s)
	r, _ := testsOf(s).History().Last(testrun.ByUser)
	if r == nil || r.Mode != testrun.ModeCoverage.String() || len(r.Scope) != 1 || !strings.HasSuffix(r.Scope[0], "TestRoundHalfEven") {
		t.Fatalf("the run: %+v", r)
	}

	s.RightClick(signPoint(t, s, "8", "▶"))
	s.ClickOn("Run file")
	runDone(t, s)
	if r, _ := testsOf(s).History().Last(testrun.ByUser); r == nil || r.Trigger != "file" || len(r.Scope) < 3 {
		t.Fatalf("Run file ran %+v", r)
	}
}

// The menu has no "Debug test" when the adapter can't debug.
func TestTestSignMenuWithoutDebug(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"adder\"\nversion = \"0.1.0\"\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	lib := filepath.Join(dir, "src", "lib.rs")
	os.WriteFile(lib, []byte("#[cfg(test)]\nmod tests {\n    #[test]\n    fn adds() {\n        assert_eq!(1 + 1, 2);\n    }\n}\n"), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.Run("editor.open", registry.Args{"path": lib})
	s.WaitSyntax()
	s.Frame()
	if !s.Registry().Available("debug.launch", nil) {
		t.Fatal("no debug.launch: the menu would lack Debug test for another reason")
	}
	ds := testDecor(s, 4)
	i := slices.IndexFunc(ds, func(d uireg.Decoration) bool { return d.Kind == uireg.Sign && d.Text == "▶" })
	if i < 0 {
		t.Fatalf("no ▶ on fn adds: %+v", ds)
	}
	if strings.Contains(ds[i].Tooltip, "debug") {
		t.Errorf("the tooltip offers debugging: %q", ds[i].Tooltip)
	}
	menu := s.RightClick(signPoint(t, s, "4", "▶"))
	if !strings.Contains(menu, "Run test") || strings.Contains(menu, "Debug test") {
		t.Errorf("the ▶ menu of a cargo test:\n%s", menu)
	}
}

// The <Leader>t keys follow LazyVim's neotest extra (lua/lazyvim/plugins/extras/test/core.lua), Oriel's
// own where they don't clash; each runs its command.
func TestTestKeys(t *testing.T) {
	s, _ := testsScene(t)
	for _, k := range []struct{ keys, command string }{
		{"<Space>tt", "test.run.file"},
		{"<Space>tr", "test.run.nearest"},
		{"<Space>td", "test.debug.nearest"},
		{"<Space>tT", "test.run.all"},
		{"<Space>tl", "test.run.last"},
		{"<Space>ts", "tests.toggle"},
		{"<Space>to", "test.output"},
		{"<Space>tO", "tests.output"},
		{"<Space>tS", "test.cancel"},
		{"<Space>tw", "test.watch"},
		{"<Space>ta", "test.attach"},
		{"<Space>tf", "test.run.file"},
		{"<Space>tF", "test.run.failed"},
		{"<Space>tp", "test.run.package"},
		{"<Space>tc", "test.coverage"},
		{"]t", "test.next_failure"},
	} {
		if !s.Registry().Available(k.command, nil) && k.command != "test.debug.nearest" {
			t.Errorf("%s: no command %s", k.keys, k.command)
			continue
		}
		ran := false
		d := s.Registry().Command(registry.Command{ID: k.command, Run: func(registry.Ctx, registry.Args) error { ran = true; return nil }})
		s.Press(k.keys)
		s.Frame()
		d.Dispose()
		if !ran {
			t.Errorf("%s didn't run %s", k.keys, k.command)
		}
	}
}

// ␣to shows the nearest test's output in the Tests panel, ␣tO the whole run's; ␣ta with nothing running says so.
func TestTestOutputKeys(t *testing.T) {
	s, _ := testsScene(t)
	s.Run("test.run.file", nil)
	runDone(t, s)
	s.EditorKeys("17G")
	s.Press("<Space>to")
	s.Frame()
	s.Frame()
	wantText(t, s, "the -3.5 row's output", "OUTPUT · TestRoundHalfEven/-3.5", "got -3, want -4")
	s.Press("<Space>tO")
	s.Frame()
	if a := s.Find("whole run"); a == nil || !a.Selected {
		t.Errorf("␣tO didn't show the whole run: %+v", a)
	}
	if err := s.Headless.Press("<Space>ta"); err == nil || !strings.Contains(err.Error(), "no test running") {
		t.Errorf("␣ta with nothing running: %v", err)
	}
}
