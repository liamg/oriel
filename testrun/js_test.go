package testrun

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type jsCase struct {
	test      string
	kind      EventKind
	reason    Reason
	want, got string
	line      int // 1-based line of the failure, 0 for none
}

func checkJS(t *testing.T, recording, file string, cases []jsCase) {
	t.Helper()
	fin := finals(replay(t, recordingNamed(t, recording)))
	pkg := "$ROOT/" + file
	for _, c := range cases {
		e, ok := fin[Key(pkg, c.test)]
		if !ok {
			t.Errorf("%s: no result", c.test)
			continue
		}
		if e.Kind != c.kind {
			t.Errorf("%s: %s, want %s", c.test, e.Kind, c.kind)
			continue
		}
		if c.kind != Failed {
			continue
		}
		f := e.Failure
		if f.Reason != c.reason || f.Want != c.want || f.Got != c.got {
			t.Errorf("%s: %s want %q got %q, want %s %q %q", c.test, f.Reason, f.Want, f.Got, c.reason, c.want, c.got)
		}
		line := 0
		if f.Loc != nil {
			if f.Loc.File != pkg {
				t.Errorf("%s: loc in %s, want the test file", c.test, f.Loc.File)
			}
			line = f.Loc.Line + 1
		}
		if line != c.line {
			t.Errorf("%s: failure on line %d, want %d", c.test, line, c.line)
		}
	}
}

// TestVitestRecordedRun reads vitest's json reporter.
//
// ref: vitest/docs/guide/reporters.md#JSON Reporter
// quote: "Outputs a report of the test results in JSON format."
func TestVitestRecordedRun(t *testing.T) {
	checkJS(t, "vitest-basic", "test/math.test.ts", []jsCase{
		{test: "passes", kind: Passed},
		{test: "fails", kind: Failed, reason: ReasonAssert, want: "5", got: "4", line: 10},
		{test: "equals an object", kind: Failed, reason: ReasonAssert, want: "{ total: 3 }", got: "{ total: 2 }", line: 14},
		{test: "throws", kind: Failed, reason: ReasonPanic, line: 18},
		{test: "times out", kind: Failed, reason: ReasonTimeout},
		{test: "skipped", kind: Skipped},
		{test: "later", kind: Skipped},
		{test: "outer > inner > nested fail", kind: Failed, reason: ReasonAssert, want: "3", got: "2", line: 35},
		{test: "parallel > b", kind: Passed},
		{test: "add(2, 2) -> 5", kind: Failed, reason: ReasonAssert, want: "5", got: "4", line: 49},
		{test: "suite y > works", kind: Passed},
		{test: "word two words", kind: Failed, reason: ReasonAssert, line: 66},
	})
}

// TestJestRecordedRun reads jest --json.
//
// ref: jest/docs/CLI.md#--json
// quote: "Prints the test results in JSON. This mode will send all other test output and user messages to stderr."
func TestJestRecordedRun(t *testing.T) {
	checkJS(t, "jest-basic", "math.test.js", []jsCase{
		{test: "passes", kind: Passed},
		{test: "fails", kind: Failed, reason: ReasonAssert, want: "5", got: "4", line: 9},
		{test: "throws", kind: Failed, reason: ReasonPanic, line: 17},
		{test: "times out", kind: Failed, reason: ReasonTimeout, line: 20},
		{test: "skipped", kind: Skipped},
		{test: "outer > inner > nested fail", kind: Failed, reason: ReasonAssert, want: "3", got: "2", line: 34},
		{test: "parallel > a", kind: Passed},
		{test: "add(2, 2) -> 5", kind: Failed, reason: ReasonAssert, want: "5", got: "4", line: 48},
		{test: "object 0 + 0", kind: Failed, reason: ReasonAssert, want: "1", got: "0", line: 55},
	})
}

// TestJSDiscoverMatchesRun checks discovery against the names each runner gave the fixture's tests, .each rows
// included.
//
// ref: vitest/docs/api/index.md#test.each
// quote: "You can also access object properties with `$` prefix, if you are using objects as arguments"
// ref: jest/docs/GlobalAPI.md#test.eachtablename-fn-timeout
// quote: "Generate unique test titles by positionally injecting parameters with"
func TestJSDiscoverMatchesRun(t *testing.T) {
	for _, c := range []struct {
		adapter   Adapter
		recording string
		file      string
		lines     map[string]int
	}{
		{&Vitest{}, "vitest-basic", "testdata/vitest/basic/test/math.test.ts", map[string]int{
			"outer > inner > nested pass": 31, "add(2, 2) -> 5": 47, "object +0 + +0": 54, "suite y > works": 60, "word two words": 65,
		}},
		{&Jest{}, "jest-basic", "testdata/jest/basic/math.test.js", map[string]int{
			"outer > inner > nested pass": 30, "object 0 + 0": 53, "parallel > b": 41,
		}},
	} {
		t.Run(c.recording, func(t *testing.T) {
			items := discoverFile(t, c.adapter, c.file)
			var got []string
			for _, it := range items {
				if it.Kind != KindSuite {
					got = append(got, it.Name)
				}
			}
			var ran []string
			for _, e := range replay(t, recordingNamed(t, c.recording)) {
				if e.Kind == Started && e.Test != "" {
					ran = append(ran, e.Test)
				}
			}
			slices.Sort(got)
			slices.Sort(ran)
			if !slices.Equal(got, ran) {
				t.Errorf("discovered %q\nran        %q", got, ran)
			}
			for name, line := range c.lines {
				if it := itemNamed(t, items, name); it.Line+1 != line {
					t.Errorf("%s on line %d, want %d", name, it.Line+1, line)
				}
			}
			abs, _ := filepath.Abs(c.file)
			if it := itemNamed(t, items, "outer > inner"); it.Kind != KindSuite || it.Package != abs {
				t.Errorf("outer > inner = %+v", it)
			}
		})
	}
}

// TestJSSelected runs one nested test and one .each row by name pattern; the others are skipped.
//
// ref: vitest/docs/config/index.md
// quote: "**CLI:** `-t <pattern>`, `--testNamePattern=<pattern>`"
// ref: jest/docs/CLI.md#--testnamepatternregex
// quote: "Run only tests with a name that matches the regex."
func TestJSSelected(t *testing.T) {
	for _, rec := range []string{"vitest-selected", "jest-selected"} {
		var ran []string
		for k, e := range finals(replay(t, recordingNamed(t, rec))) {
			if e.Test != "" && e.Kind != Skipped {
				_, name, _ := strings.Cut(k, "\x00")
				ran = append(ran, name)
			}
		}
		slices.Sort(ran)
		if want := []string{"add(2, 2) -> 5", "outer > inner > nested pass"}; !slices.Equal(ran, want) {
			t.Errorf("%s ran %q, want %q", rec, ran, want)
		}
	}
	root := fixtureRoot(t, "vitest/basic")
	items := jsSelected("vitest", "test/math.test.ts")(root)
	cmd, _ := (&Vitest{}).Command(Workspace{Root: root}, items, ModeRun)
	if p := cmd.Args[len(cmd.Args)-2]; p != `^(?:outer inner nested pass$|add\(2, 2\) -> 5$)` {
		t.Errorf("pattern = %q", p)
	}
}

// TestVitestRelated runs the tests that import a saved file.
//
// ref: vitest/docs/guide/cli.md#vitest-related
// quote: "Run only tests that cover a list of source files."
func TestVitestRelated(t *testing.T) {
	root := fixtureRoot(t, "vitest/basic")
	src := filepath.Join(root, "src", "math.ts")
	items, err := (&Vitest{}).Affected(Workspace{Root: root}, src)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := (&Vitest{}).Command(Workspace{Root: root}, items, ModeWatch)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(root, "node_modules", ".bin", "vitest"), "related", "src/math.ts", "--run", "--reporter=json"}; !slices.Equal(cmd.Args, want) && !slices.Equal(cmd.Args[1:], append([]string{"--no-install", "vitest"}, want[1:]...)) {
		t.Errorf("args = %q", cmd.Args)
	}
	fin := finals(replay(t, recordingNamed(t, "vitest-related")))
	if _, ok := fin[Key("$ROOT/test/math.test.ts", "passes")]; !ok {
		t.Errorf("the related run didn't run the test that imports src/math.ts: %v", fin)
	}
}

func TestJSCommands(t *testing.T) {
	root := fixtureRoot(t, "jest/basic")
	f := filepath.Join(root, "math.test.js")
	cmd, err := (&Jest{}).Command(Workspace{Root: root}, []Item{{Package: f, Name: "outer > inner", Path: []string{"outer", "inner"}, File: f, Kind: KindSuite}}, ModeCoverage)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args[1:], " ")
	want := "--json --testLocationInResults --coverage --coverageReporters=json --coverageDirectory=" + RunDir + "/coverage --testNamePattern ^(?:outer inner ) --runTestsByPath math.test.js"
	if !strings.HasSuffix(args, want) || cmd.CoverFile != "coverage/coverage-final.json" {
		t.Errorf("args = %q\nwant   %q", args, want)
	}
	if !(&Jest{}).Detect(Workspace{Root: root}) || (&Vitest{}).Detect(Workspace{Root: root}) {
		t.Error("the jest fixture is jest's, not vitest's")
	}
	if v := fixtureRoot(t, "vitest/basic"); !(&Vitest{}).Detect(Workspace{Root: v}) || (&Jest{}).Detect(Workspace{Root: v}) {
		t.Error("the vitest fixture is vitest's, not jest's")
	}
}

// TestIstanbulCoverage reads coverage-final.json from both runners.
//
// ref: vitest/docs/config/index.md
// quote: "Generate coverage report even when tests fail."
func TestIstanbulCoverage(t *testing.T) {
	for _, c := range []struct {
		recording, fixture, file string
		adapter                  Coverer
	}{
		{"vitest-cover", "vitest/basic", "src/math.ts", &Vitest{}},
		{"jest-cover", "jest/basic", "math.js", &Jest{}},
	} {
		root := fixtureRoot(t, c.fixture)
		rec := loadRecording(t, c.recording)
		cov, err := c.adapter.Coverage(Workspace{Root: root}, strings.NewReader(strings.ReplaceAll(string(rec.cover), "$ROOT", root)))
		if err != nil {
			t.Fatal(err)
		}
		lines := cov.Files[filepath.Join(root, filepath.FromSlash(c.file))]
		// add's body runs; sign never gets a negative number, so its return -1 doesn't.
		for line, hit := range map[int]bool{2: true, 7: false, 9: true} {
			if n, ok := lines[line-1]; !ok || (n > 0) != hit {
				t.Errorf("%s %s:%d = %d (measured %v), want covered=%v", c.recording, c.file, line, n, ok, hit)
			}
		}
	}
}
