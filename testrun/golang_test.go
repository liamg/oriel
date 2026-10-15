package testrun

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/liamg/oriel/text"
)

func discoverFile(t *testing.T, a Adapter, path string) []Item {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	return a.Discover(Source{Path: abs, Text: text.New(b, text.Author{}).Snapshot()})
}

func itemNames(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Name)
	}
	return out
}

func itemNamed(t *testing.T, items []Item, name string) Item {
	t.Helper()
	for _, it := range items {
		if it.Name == name {
			return it
		}
	}
	t.Fatalf("no item %q in %q", name, itemNames(items))
	return Item{}
}

// TestGoRecordedRun checks the events read from a real go test -json run of every case.
//
// ref: go/doc-test2json.txt
// quote: "fail - the test or benchmark failed"
// ref: go/doc-test2json.txt
// quote: "The FailedBuild field is set for Action == \"fail\" if the test failure was caused by a build failure."
func TestGoRecordedRun(t *testing.T) {
	evs := replay(t, recordingNamed(t, "go-basic"))
	fin := finals(evs)
	const calc = "example.com/basic/calc"
	for _, c := range []struct {
		pkg, test string
		kind      EventKind
		reason    Reason
		want, got string
		loc       string // file:line, 1-based as printed
		message   string
	}{
		{pkg: calc, test: "TestPass", kind: Passed},
		{pkg: calc, test: "TestGotWant", kind: Failed, reason: ReasonAssert, want: "5", got: "4", loc: "calc_test.go:16", message: "Add(2, 2): got 4, want 5"},
		{pkg: calc, test: "TestWantGot", kind: Failed, reason: ReasonAssert, want: `"five"`, got: `"4"`, loc: "calc_test.go:20"},
		{pkg: calc, test: "TestSkip", kind: Skipped, message: "not today"},
		{pkg: calc, test: "TestNested/outer/inner_pass", kind: Passed},
		{pkg: calc, test: "TestNested/outer/inner_fail", kind: Failed, reason: ReasonAssert, want: "2", got: "1", loc: "calc_test.go:36"},
		{pkg: calc, test: "TestNested", kind: Failed, reason: ReasonAssert},
		{pkg: calc, test: "TestParallel/a", kind: Passed},
		{pkg: calc, test: "TestParallel/b", kind: Failed, reason: ReasonAssert, loc: "calc_test.go:47", message: "b failed"},
		{pkg: calc, test: "TestTable/two_plus_two", kind: Failed, reason: ReasonAssert, want: "5", got: "4", loc: "calc_test.go:66"},
		{pkg: calc, test: "TestDuplicate/same#01", kind: Passed},
		{pkg: calc, test: "TestDuplicate/#00", kind: Passed},
		{pkg: "example.com/basic/panics", test: "TestPanics", kind: Failed, reason: ReasonPanic, loc: "$ROOT/panics/panics_test.go:9", message: "assignment to entry in nil map"},
		{pkg: "example.com/basic/slow", test: "TestSlow", kind: Failed, reason: ReasonTimeout, loc: "$ROOT/slow/slow_test.go:11", message: "test timed out after 3s"},
		{pkg: "example.com/basic/slow", test: "TestQuick", kind: Passed},
		{pkg: "example.com/basic/broken", kind: BuildFailed, reason: ReasonBuild, loc: "broken/broken_test.go:6"},
		{pkg: "example.com/basic/panics", kind: Failed, reason: ReasonPanic},
	} {
		e, ok := fin[Key(c.pkg, c.test)]
		if !ok {
			t.Errorf("%s %s: no result", c.pkg, c.test)
			continue
		}
		if e.Kind != c.kind {
			t.Errorf("%s: %s, want %s", c.test, e.Kind, c.kind)
			continue
		}
		if c.kind == Skipped && e.Message != c.message {
			t.Errorf("%s: skip message %q, want %q", c.test, e.Message, c.message)
		}
		if c.kind != Failed && c.kind != BuildFailed {
			continue
		}
		f := e.Failure
		if f == nil {
			t.Errorf("%s: no failure", c.test)
			continue
		}
		if f.Reason != c.reason || f.Want != c.want || f.Got != c.got {
			t.Errorf("%s: failure %+v, want reason %s want %q got %q", c.test, f, c.reason, c.want, c.got)
		}
		if c.message != "" && f.Message != c.message {
			t.Errorf("%s: message %q, want %q", c.test, f.Message, c.message)
		}
		if loc := ""; f.Loc != nil || c.loc != "" {
			if f.Loc != nil {
				loc = f.Loc.String()
			}
			if loc != c.loc {
				t.Errorf("%s: loc %q, want %q", c.test, loc, c.loc)
			}
		}
	}
	if _, ok := fin[Key("example.com/basic/panics", "TestAfter")]; ok {
		t.Error("TestAfter has a result, but the panic stopped the test binary before it ran")
	}
	// Each test's output reaches it, a line at a time.
	var logs []string
	for _, e := range evs {
		if e.Kind == Output && e.Test == "TestLogs" {
			logs = append(logs, e.Line)
		}
	}
	if !slices.Contains(logs, "    calc_test.go:28: first line") || !slices.Contains(logs, "printed to stdout") {
		t.Errorf("TestLogs output = %q", logs)
	}
}

// TestGoPartialOutput joins output that comes in pieces of a line.
//
// ref: go/doc-test2json.txt
// quote: "The Output field is set for Action == \"output\" and is a portion of the test's output"
func TestGoPartialOutput(t *testing.T) {
	in := `{"Action":"run","Package":"p","Test":"TestA"}
{"Action":"output","Package":"p","Test":"TestA","Output":"    a_test.go:3: got 1, "}
{"Action":"output","Package":"p","Test":"TestA","Output":"want 2\n"}
{"Action":"fail","Package":"p","Test":"TestA","Elapsed":0.25}
`
	var evs []Event
	if err := (&Go{}).Parse(strings.NewReader(in), func(e Event) { evs = append(evs, e) }); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, e := range evs {
		if e.Kind == Output {
			lines = append(lines, e.Line)
		}
	}
	if len(lines) != 1 || lines[0] != "    a_test.go:3: got 1, want 2" {
		t.Errorf("output lines = %q", lines)
	}
	last := evs[len(evs)-1]
	if last.Kind != Failed || last.Elapsed.Milliseconds() != 250 || last.Failure.Got != "1" || last.Failure.Want != "2" {
		t.Errorf("last event = %+v %+v", last, last.Failure)
	}
}

// TestGoDiscoverMatchesRun checks that every test, subtest and table row found in the fixture's source has the
// name go test gave it when it ran, and that none it ran is missing.
//
// ref: go/doc-testing.txt
// quote: "where Xxx does not start with a lowercase letter."
// ref: go/src/testing/match.go#rewrite
// quote: "rewrite rewrites a subname to having only printable characters and no white space."
// ref: go/src/testing/match.go#matcher.unique
// quote: "unique creates a unique name for the given parent and subname by affixing it with one or more counts, if necessary."
func TestGoDiscoverMatchesRun(t *testing.T) {
	items := discoverFile(t, &Go{}, "testdata/go/basic/calc/calc_test.go")
	var ran []string
	for _, e := range replay(t, recordingNamed(t, "go-basic")) {
		if e.Kind == Started && e.Package == "example.com/basic/calc" && e.Test != "" {
			ran = append(ran, e.Test)
		}
	}
	got := itemNames(items)
	sort.Strings(got)
	sort.Strings(ran)
	if !slices.Equal(got, ran) {
		t.Errorf("discovered %q\nran        %q", got, ran)
	}
	for _, it := range items {
		if it.Package != "example.com/basic/calc" {
			t.Errorf("%s: package %q", it.Name, it.Package)
		}
	}
	for _, c := range []struct {
		name string
		line int // 1-based
		kind Kind
		path []string
	}{
		{"TestPass", 8, KindTest, []string{"TestPass"}},
		{"TestNested/outer/inner_pass", 34, KindSubtest, []string{"TestNested", "outer", "inner pass"}},
		{"TestTable/one_plus_one", 56, KindRow, []string{"TestTable", "one plus one"}},
		{"TestTable/two_plus_two", 57, KindRow, nil},
		{"TestTable/zero", 59, KindRow, nil},
		{"TestMapTable/ten/two", 74, KindRow, []string{"TestMapTable", "ten/two"}},
		{"TestDuplicate/same#01", 88, KindSubtest, nil},
		{"TestDuplicate/#00", 89, KindSubtest, nil},
	} {
		it := itemNamed(t, items, c.name)
		if it.Line+1 != c.line || it.Kind != c.kind || c.path != nil && !slices.Equal(it.Path, c.path) {
			t.Errorf("%s: line %d kind %s path %q, want line %d kind %s path %q", c.name, it.Line+1, it.Kind, it.Path, c.line, c.kind, c.path)
		}
	}
}

func TestGoDiscoverEdges(t *testing.T) {
	items := discoverFile(t, &Go{}, "testdata/go/discover/names_test.go")
	want := []string{
		"Test",
		"TestÉclair",
		"TestRegexChars",
		"TestRegexChars/a+b_(c)",
		`TestRegexChars/raw\name`,
		"TestPositional",
		"TestPositional/one",
		"TestPositional/two",
		"TestComputedName",
		"TestComputedName/kept",
	}
	if got := itemNames(items); !slices.Equal(got, want) {
		t.Errorf("discovered %q\nwant       %q", got, want)
	}
	if it := itemNamed(t, items, "TestPositional/two"); it.Line+1 != 26 {
		t.Errorf("TestPositional/two on line %d, want 26", it.Line+1)
	}
}

// TestGoRunPattern selects tests by name, a regular expression per level, quoted.
//
// ref: go/help-testflag.txt
// quote: "For tests, the regular expression is split by unbracketed slash (/) characters into a sequence of regular expressions, and each part of a test's identifier must match the corresponding element in the sequence, if any."
func TestGoRunPattern(t *testing.T) {
	for _, c := range []struct {
		names []string
		want  string
	}{
		{[]string{"TestA"}, "^TestA$"},
		{[]string{"TestA/a+b_(c)"}, `^TestA$/^a\+b_\(c\)$`},
		{[]string{"TestMapTable/ten/two"}, "^TestMapTable$/^ten$/^two$"},
		{[]string{"TestA", "TestB"}, "^(TestA|TestB)$"},
		// Cut to the shallowest so TestA's subtests all run.
		{[]string{"TestA", "TestB/x"}, "^(TestA|TestB)$"},
		{[]string{"TestA/x", "TestA/y"}, "^TestA$/^(x|y)$"},
	} {
		if got := goRunPattern(c.names); got != c.want {
			t.Errorf("goRunPattern(%q) = %q, want %q", c.names, got, c.want)
		}
	}
	// The recorded run of one nested subtest ran it and its parents, and nothing else.
	var ran []string
	for _, e := range replay(t, recordingNamed(t, "go-selected")) {
		if e.Kind == Started && e.Test != "" {
			ran = append(ran, e.Test)
		}
	}
	if want := []string{"TestNested", "TestNested/outer", "TestNested/outer/inner_pass"}; !slices.Equal(ran, want) {
		t.Errorf("ran %q, want %q", ran, want)
	}
	rec := loadRecording(t, "go-selected")
	if args := rec.header["args"]; len(args) != 1 || args[0] != "go test -json -run ^TestNested$/^outer$/^inner_pass$ ./calc" {
		t.Errorf("recorded args = %q", args)
	}
}

func TestGoCommand(t *testing.T) {
	root := fixtureRoot(t, "go/basic")
	ws := Workspace{Root: root}
	g := &Go{Flags: []string{"-race"}}
	calc := filepath.Join(root, "calc", "calc_test.go")
	cmd, err := g.Command(ws, []Item{{Name: "TestPass", File: calc}}, ModeCoverage)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"go", "test", "-json", "-race", "-run", "^TestPass$", "-coverprofile=" + RunDir + "/cover.out", "./calc"}
	if !slices.Equal(cmd.Args, want) || cmd.Dir != root || cmd.CoverFile != "cover.out" {
		t.Errorf("command = %q in %s cover %q, want %q", cmd.Args, cmd.Dir, cmd.CoverFile, want)
	}
	cmd, _ = g.Command(ws, nil, ModeRun)
	if !slices.Equal(cmd.Args, []string{"go", "test", "-json", "-race", "./..."}) {
		t.Errorf("everything = %q", cmd.Args)
	}
	cmd, _ = g.Command(ws, []Item{{Kind: KindPackage, File: filepath.Join(root, "panics")}, {Name: "TestPass", File: calc}}, ModeWatch)
	if !slices.Equal(cmd.Args, []string{"go", "test", "-json", "-race", "./panics", "./calc"}) {
		t.Errorf("a whole package = %q", cmd.Args)
	}
}

// TestGoDebugLaunch debugs one table row: delve's test mode on its package, the test binary told to run only it.
//
// ref: go/help-testflag.txt
// quote: "Each of these flags is also recognized with an optional 'test.' prefix,"
func TestGoDebugLaunch(t *testing.T) {
	root := fixtureRoot(t, "go/basic")
	calc := filepath.Join(root, "calc", "calc_test.go")
	cmd, err := (&Go{}).Command(Workspace{Root: root}, []Item{{Name: "TestTable/a+b", File: calc, Kind: KindRow}}, ModeDebug)
	if err != nil {
		t.Fatal(err)
	}
	l := cmd.Launch
	if l == nil {
		t.Fatal("no launch for a debug run")
	}
	dir := filepath.Join(root, "calc")
	if l.Type != "go" || l.Program != dir || l.Cwd != dir || l.Extra["mode"] != "test" {
		t.Errorf("launch = %+v", l)
	}
	if want := []string{"-test.v", "-test.run", `^TestTable$/^a\+b$`}; !slices.Equal(l.Args, want) {
		t.Errorf("args = %q, want %q", l.Args, want)
	}
	if l.Name != "go test -run TestTable/a+b" {
		t.Errorf("name = %q", l.Name)
	}
	if _, err := (&Go{}).Command(Workspace{Root: root}, []Item{{Name: "TestA", File: calc}, {Name: "TestB", File: filepath.Join(root, "panics", "x_test.go")}},
		ModeDebug); err == nil {
		t.Error("two packages can't be debugged in one session")
	}
}

// TestGoCoverage reads the recorded -coverprofile into lines per file.
//
// ref: go/src/cmd/vendor/golang.org/x/tools/cover/profile.go#ParseProfilesFromReader
// quote: "where the fields are: name.go:line.column,line.column numberOfStatements count"
func TestGoCoverage(t *testing.T) {
	root := fixtureRoot(t, "go/watch")
	rec := loadRecording(t, "go-cover")
	ws := Workspace{Root: root, Env: []string{"GOWORK=off"}}
	cov, err := (&Go{}).Coverage(ws, strings.NewReader(strings.ReplaceAll(string(rec.cover), "$ROOT", root)))
	if err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(root, "a", "a.go")
	lines := cov.Files[a]
	if lines == nil {
		t.Fatalf("no coverage for %s in %v", a, cov.Files)
	}
	// Sign(-3) returns at line 7; lines 10 and 12 never run.
	for line, hit := range map[int]bool{3: true, 6: true, 7: true, 10: false, 12: false} {
		if n, ok := lines[line-1]; !ok || (n > 0) != hit {
			t.Errorf("a.go:%d hits %d (measured %v), want covered=%v", line, n, ok, hit)
		}
	}
	if r := cov.Ranges(a); len(r) == 0 || !r[0].Covered {
		t.Errorf("ranges = %+v", r)
	}
	if !bytes.HasPrefix(rec.cover, []byte("mode: set\n")) {
		t.Errorf("profile starts %q", rec.cover[:min(20, len(rec.cover))])
	}
}
