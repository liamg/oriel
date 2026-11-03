package testrun

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestPytestRecordedRun checks the events the bundled plugin reported from a real pytest run of every case.
//
// ref: pytest/doc/en/reference/reference.rst
// quote: "-p name Early-load given plugin module name or entry point"
// ref: pytest/src/_pytest/hookspec.py
// quote: "Process the :class:`~pytest.TestReport` produced for each of the setup, call and teardown runtest phases of an item."
func TestPytestRecordedRun(t *testing.T) {
	evs := replay(t, recordingNamed(t, "pytest-basic"))
	fin := finals(evs)
	const f = "tests/test_calc.py"
	for _, c := range []struct {
		test      string
		kind      EventKind
		reason    Reason
		want, got string
		loc       string
		message   string
	}{
		{test: "test_pass", kind: Passed},
		{test: "test_fail", kind: Failed, reason: ReasonAssert, want: "5", got: "4", loc: "$ROOT/tests/test_calc.py:12"},
		{test: "test_raises", kind: Failed, reason: ReasonPanic, loc: "$ROOT/calc.py:7", message: "ZeroDivisionError: b is zero"},
		{test: "test_skip", kind: Skipped, message: "not today"},
		{test: "test_skip_inside", kind: Skipped, message: "skipped inside"},
		{test: "test_xfail", kind: Skipped, message: "xfail: known bug"},
		{test: "test_setup_error", kind: Failed, reason: ReasonError, loc: "$ROOT/tests/test_calc.py:35", message: "RuntimeError: fixture broke"},
		{test: "TestNested::test_inner", kind: Passed},
		{test: "TestNested::TestDeeper::test_deep", kind: Passed},
		{test: "test_add_table[2-2-5]", kind: Failed, reason: ReasonAssert, want: "5", got: "4", loc: "$ROOT/tests/test_calc.py:64"},
		{test: "test_add_table[-1-1-0]", kind: Passed},
		{test: "test_words[two words]", kind: Failed, reason: ReasonAssert},
		{test: "test_words[custom-id]", kind: Passed},
		{test: "test_stacked[3-one]", kind: Passed},
	} {
		e, ok := fin[Key(f, c.test)]
		if !ok {
			t.Errorf("%s: no result", c.test)
			continue
		}
		if e.Kind != c.kind {
			t.Errorf("%s: %s, want %s", c.test, e.Kind, c.kind)
			continue
		}
		if c.kind == Skipped {
			if e.Message != c.message {
				t.Errorf("%s: skipped %q, want %q", c.test, e.Message, c.message)
			}
			continue
		}
		if c.kind != Failed {
			continue
		}
		fl := e.Failure
		if fl.Reason != c.reason || fl.Want != c.want || fl.Got != c.got {
			t.Errorf("%s: failure %s want %q got %q, want %s %q %q", c.test, fl.Reason, fl.Want, fl.Got, c.reason, c.want, c.got)
		}
		if c.loc != "" && (fl.Loc == nil || fl.Loc.String() != c.loc) {
			t.Errorf("%s: loc %v, want %s", c.test, fl.Loc, c.loc)
		}
		if c.message != "" && fl.Message != c.message {
			t.Errorf("%s: message %q, want %q", c.test, fl.Message, c.message)
		}
	}
	var out []string
	for _, e := range evs {
		if e.Kind == Output && e.Test == "test_fail" {
			out = append(out, e.Line)
		}
	}
	if !slices.Equal(out, []string{"about to add"}) {
		t.Errorf("test_fail's output = %q", out)
	}
	// The recording was cut off while test_slow ran: it started and never finished, so the run times it out.
	slow := Key("tests/test_zslow.py", "test_slow")
	if _, ok := fin[slow]; ok {
		t.Error("test_slow has a result in a run killed while it ran")
	}
	run := &Run{}
	for _, e := range evs {
		run.Apply(e)
	}
	run.finishUnfinished(ReasonTimeout, "run timed out", run.Apply)
	if r, ok := run.Result(slow); !ok || r.Status != StatusTimedOut {
		t.Errorf("test_slow = %+v", r)
	}
	if _, ok := run.Result(Key("tests/test_zslow.py", "test_after_slow")); ok {
		t.Error("test_after_slow never started, so it has no result")
	}
}

func TestPytestCollectionError(t *testing.T) {
	fin := finals(replay(t, recordingNamed(t, "pytest-broken")))
	e, ok := fin[Key("tests/test_broken_import.py", "")]
	if !ok || e.Kind != BuildFailed || e.Failure.Message != "ModuleNotFoundError: No module named 'not_a_module_anywhere'" || e.Failure.Loc.String() != "tests/test_broken_import.py:1" {
		t.Errorf("collection error = %+v %+v", e, e.Failure)
	}
}

// TestPytestDiscoverMatchesCollect compares what discovery finds with pytest's own --collect-only.
//
// ref: pytest/doc/en/explanation/goodpractices.rst#Conventions for Python test discovery
// quote: "test prefixed test functions or methods inside Test prefixed test classes (without an __init__ method)."
// ref: pytest/doc/en/example/parametrize.rst#Different options for test IDs
// quote: "Numbers, strings, booleans and None will have their usual string representation used in the test ID. For other objects, pytest will make a string based on the argument name"
func TestPytestDiscoverMatchesCollect(t *testing.T) {
	items := discoverFile(t, &Pytest{}, "testdata/python/basic/tests/test_calc.py")
	parents := map[string]bool{}
	for _, it := range items {
		if it.Kind == KindRow {
			parents[it.Path[len(it.Path)-2]] = true
		}
	}
	var got []string
	for _, it := range items {
		if it.Kind == KindSuite || it.Kind == KindTest && parents[it.Path[len(it.Path)-1]] {
			continue
		}
		got = append(got, it.Package+"::"+it.Name)
	}
	var want []string
	for _, l := range readLines(loadRecording(t, "pytest-collect").body) {
		if strings.HasPrefix(l, "tests/test_calc.py::") {
			want = append(want, l)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("discovered %q\npytest     %q", got, want)
	}
	for _, c := range []struct {
		name string
		line int
		kind Kind
	}{
		{"test_pass", 6, KindTest},
		{"TestNested", 42, KindSuite},
		{"TestNested::TestDeeper::test_deep", 47, KindTest},
		{"test_add_table[2-2-5]", 62, KindRow},
		{"test_words[two words]", 71, KindRow},
		{"test_words[custom-id]", 72, KindRow},
		{"test_stacked[3-one]", 84, KindRow},
	} {
		it := itemNamed(t, items, c.name)
		if it.Line+1 != c.line || it.Kind != c.kind {
			t.Errorf("%s: line %d %s, want line %d %s", c.name, it.Line+1, it.Kind, c.line, c.kind)
		}
	}
	for _, it := range items {
		if strings.Contains(it.Name, "TestWithInit") || strings.Contains(it.Name, "helper") {
			t.Errorf("%s isn't a test", it.Name)
		}
	}
}

func TestPytestCommand(t *testing.T) {
	root := fixtureRoot(t, "python/basic")
	file := filepath.Join(root, "tests", "test_calc.py")
	cmd, err := (&Pytest{Python: "py"}).Command(Workspace{Root: root}, []Item{
		{Package: "tests/test_calc.py", Name: "TestNested::test_inner", File: file},
		{Package: "tests/test_calc.py", Name: "test_words[two words]", File: file, Kind: KindRow},
	}, ModeCoverage)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"py", "-m", "pytest", "-p", "oriel_pytest", "--cov=" + root, "--cov-report=json:" + RunDir + "/coverage.json",
		file + "::TestNested::test_inner", file + "::test_words[two words]"}
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("args = %q\nwant   %q", cmd.Args, want)
	}
	if string(cmd.Files["oriel_pytest.py"]) != string(pytestPlugin) || cmd.CoverFile != "coverage.json" {
		t.Error("the plugin isn't written for the run")
	}
	if !slices.ContainsFunc(cmd.Env, func(e string) bool { return strings.HasPrefix(e, "PYTHONPATH="+RunDir) }) {
		t.Errorf("env = %q: the run's directory must be on PYTHONPATH", cmd.Env)
	}
}

// TestPytestDebugLaunch debugs one parametrized case: pytest as debugpy's module, given only that case's node id.
//
// ref: pytest/doc/en/explanation/goodpractices.rst
// quote: "command line arguments can be used in any combination of directories, file names or node ids."
func TestPytestDebugLaunch(t *testing.T) {
	root := fixtureRoot(t, "python/basic")
	file := filepath.Join(root, "tests", "test_calc.py")
	cmd, err := (&Pytest{}).Command(Workspace{Root: root}, []Item{{Package: "tests/test_calc.py", Name: "test_words[two words]", File: file, Kind: KindRow}}, ModeDebug)
	if err != nil {
		t.Fatal(err)
	}
	l := cmd.Launch
	if l == nil {
		t.Fatal("no launch for a debug run")
	}
	if l.Type != "python" || l.Program != "" || l.Cwd != root || l.Extra["module"] != "pytest" || l.Extra["python"] != nil {
		t.Errorf("launch = %+v", l)
	}
	if want := []string{file + "::test_words[two words]"}; !slices.Equal(l.Args, want) {
		t.Errorf("args = %q, want %q", l.Args, want)
	}
	if l.Name != "pytest tests/test_calc.py::test_words[two words]" {
		t.Errorf("name = %q", l.Name)
	}
}

// TestPytestCoverage reads pytest-cov's JSON report.
//
// ref: pytest-cov/docs/reporting.rst
// quote: "--cov-report json:cov.json"
func TestPytestCoverage(t *testing.T) {
	root := fixtureRoot(t, "python/basic")
	rec := loadRecording(t, "pytest-cover")
	cov, err := (&Pytest{}).Coverage(Workspace{Root: root}, strings.NewReader(strings.ReplaceAll(string(rec.cover), "$ROOT", root)))
	if err != nil {
		t.Fatal(err)
	}
	calc := cov.Files[filepath.Join(root, "calc.py")]
	for line, hit := range map[int]bool{1: true, 2: true, 5: true, 6: false, 7: false} {
		if n, ok := calc[line-1]; !ok || (n > 0) != hit {
			t.Errorf("calc.py:%d = %d (measured %v), want covered=%v", line, n, ok, hit)
		}
	}
	// The lines agree with coverage.py's own summary of each file.
	var doc struct {
		Files map[string]struct {
			Summary struct {
				Covered    int `json:"covered_lines"`
				Statements int `json:"num_statements"`
			} `json:"summary"`
		} `json:"files"`
	}
	if err := json.Unmarshal(rec.cover, &doc); err != nil {
		t.Fatal(err)
	}
	for name, f := range doc.Files {
		lines := cov.Files[filepath.Join(root, name)]
		covered := 0
		for _, n := range lines {
			if n > 0 {
				covered++
			}
		}
		if len(lines) != f.Summary.Statements || covered != f.Summary.Covered {
			t.Errorf("%s: %d lines, %d covered; coverage.py says %d, %d", name, len(lines), covered, f.Summary.Statements, f.Summary.Covered)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".coverage")); err == nil {
		t.Error("pytest-cov left .coverage in the project: COVERAGE_FILE should put it in the run's directory")
	}
}

func TestPytestAffected(t *testing.T) {
	root := fixtureRoot(t, "python/basic")
	ws := Workspace{Root: root}
	p := &Pytest{}
	for saved, want := range map[string][]string{
		"calc.py":             {"tests/test_calc.py"},
		"tests/test_zslow.py": {"tests/test_zslow.py"},
		"tests/conftest.py":   {"tests"},
		"README.md":           nil,
	} {
		items, err := p.Affected(ws, filepath.Join(root, saved))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, it := range items {
			got = append(got, ws.Rel(it.File))
		}
		if !slices.Equal(got, want) {
			t.Errorf("saving %s selects %q, want %q", saved, got, want)
		}
	}
}
