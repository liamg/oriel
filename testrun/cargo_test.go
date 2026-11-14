package testrun

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type cargoCase struct {
	pkg, test string
	kind      EventKind
	reason    Reason
	want, got string
	loc       string
}

var cargoCases = []cargoCase{
	{pkg: "lib", test: "tests::passes", kind: Passed},
	{pkg: "lib", test: "tests::fails", kind: Failed, reason: ReasonAssert, want: "5", got: "4", loc: "src/lib.rs:26"},
	{pkg: "lib", test: "tests::panics", kind: Failed, reason: ReasonPanic, loc: "src/lib.rs:32"},
	{pkg: "lib", test: "tests::ignored", kind: Skipped},
	{pkg: "lib", test: "tests::should_panic", kind: Passed},
	{pkg: "lib", test: "tests::inner::deep", kind: Passed},
	{pkg: "lib", test: "shapes::tests::square", kind: Passed},
	{pkg: "test/integration", test: "adds", kind: Passed},
	{pkg: "test/integration", test: "fails_too", kind: Failed, reason: ReasonAssert, loc: "tests/integration.rs:8"},
	{pkg: "test/slow", test: "quick", kind: Passed},
}

func checkCargo(t *testing.T, recording string, cases []cargoCase) map[string]Event {
	t.Helper()
	fin := finals(replay(t, recordingNamed(t, recording)))
	for _, c := range cases {
		e, ok := fin[Key(c.pkg, c.test)]
		if !ok {
			t.Errorf("%s %s: no result", c.pkg, c.test)
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
		if f.Reason != c.reason || f.Want != c.want || f.Got != c.got || f.Loc == nil || f.Loc.String() != c.loc {
			t.Errorf("%s: %s want %q got %q at %v, want %s %q %q at %s", c.test, f.Reason, f.Want, f.Got, f.Loc, c.reason, c.want, c.got, c.loc)
		}
	}
	return fin
}

// TestCargoTestRecordedRun reads cargo test's own output; the run was killed while tests/slow.rs's slow test ran,
// and it's timed out from what libtest said about it.
func TestCargoTestRecordedRun(t *testing.T) {
	checkCargo(t, "cargo-test", cargoCases)
	run := &Run{}
	for _, e := range replay(t, recordingNamed(t, "cargo-test")) {
		run.Apply(e)
	}
	run.finishUnfinished(ReasonTimeout, "run timed out", run.Apply)
	if r, ok := run.Result(Key("test/slow", "slow")); !ok || r.Status != StatusTimedOut {
		t.Errorf("slow = %+v", r)
	}
}

// TestCargoNextestRecordedRun reads nextest's libtest JSON.
//
// ref: nextest/site/src/docs/machine-readable/libtest-json.md#Usage
// quote: "NEXTEST_EXPERIMENTAL_LIBTEST_JSON=1 cargo nextest run --message-format <format>"
// ref: nextest/site/src/docs/machine-readable/libtest-json.md#Usage
// quote: "Produce libtest JSON output, along with an extra `nextest` field."
func TestCargoNextestRecordedRun(t *testing.T) {
	fin := checkCargo(t, "cargo-nextest", cargoCases)
	if e := fin[Key("test/slow", "slow")]; e.Kind != Failed || e.Failure.Reason != ReasonTimeout {
		t.Errorf("slow = %+v %+v", e, e.Failure)
	}
	run := &Run{}
	for _, e := range replay(t, recordingNamed(t, "cargo-nextest")) {
		run.Apply(e)
	}
	if r, _ := run.Result(Key("lib", "")); r.Status != StatusFailed {
		t.Errorf("lib = %s: it's reported in parts, and one failed", r.Status)
	}
}

func TestCargoSelected(t *testing.T) {
	for _, rec := range []string{"cargo-test-selected", "cargo-nextest-selected"} {
		var ran []string
		for k, e := range finals(replay(t, recordingNamed(t, rec))) {
			if e.Test != "" {
				ran = append(ran, strings.ReplaceAll(k, "\x00", " "))
			}
		}
		slices.Sort(ran)
		if want := []string{"lib tests::inner::deep", "test/integration adds"}; !slices.Equal(ran, want) {
			t.Errorf("%s ran %q, want %q", rec, ran, want)
		}
	}
}

// TestCargoDiscoverMatchesRun checks the test names found in each source file against cargo test's.
//
// ref: rust-reference/src/attributes/testing.md#The `test` attribute
// quote: "The *`test` [attribute][attributes]* marks a function to be executed as a test."
func TestCargoDiscoverMatchesRun(t *testing.T) {
	var got []string
	for _, f := range []string{"src/lib.rs", "src/shapes.rs", "tests/integration.rs", "tests/slow.rs"} {
		for _, it := range discoverFile(t, &Cargo{}, filepath.Join("testdata/cargo/basic", f)) {
			if it.Kind == KindTest {
				got = append(got, it.Package+" "+it.Name)
			}
		}
	}
	var ran []string
	for _, e := range replay(t, recordingNamed(t, "cargo-test")) {
		if e.Kind == Started && e.Test != "" {
			ran = append(ran, e.Package+" "+e.Test)
		}
	}
	slices.Sort(got)
	slices.Sort(ran)
	if !slices.Equal(got, ran) {
		t.Errorf("discovered %q\nran        %q", got, ran)
	}
	items := discoverFile(t, &Cargo{}, "testdata/cargo/basic/src/lib.rs")
	if it := itemNamed(t, items, "tests::inner"); it.Kind != KindSuite || it.Line+1 != 45 {
		t.Errorf("tests::inner = %+v", it)
	}
	if it := itemNamed(t, items, "tests::fails"); it.Line+1 != 24 {
		t.Errorf("tests::fails on line %d", it.Line+1)
	}
	edges := itemNames(discoverFile(t, &Cargo{}, "testdata/cargo/discover/tokio.rs"))
	if want := []string{"async_test", "ignored_with_comment", "outer", "outer::inner", "outer::inner::deep"}; !slices.Equal(edges, want) {
		t.Errorf("discovered %q, want %q", edges, want)
	}
}

func TestCargoCommand(t *testing.T) {
	root := fixtureRoot(t, "cargo/basic")
	ws := Workspace{Root: root}
	items := append(cargoSelected(root), Item{Package: "lib", Name: "tests::inner", Kind: KindSuite})
	no, yes := false, true
	cmd, _ := (&Cargo{Nextest: &no}).Command(ws, items, ModeRun)
	if got := strings.Join(cmd.Args, " "); got != "cargo test --no-fail-fast --lib --test=integration -- tests::inner::deep adds tests::inner::" || !cmd.Stderr {
		t.Errorf("cargo test = %q", got)
	}
	cmd, _ = (&Cargo{Nextest: &yes}).Command(ws, items, ModeRun)
	want := "cargo nextest run --message-format libtest-json-plus --no-fail-fast -E (binary_id(basic) & test(=tests::inner::deep)) | (binary_id(basic::integration) & test(=adds)) | (binary_id(basic) & test(/^tests::inner::/))"
	if got := strings.Join(cmd.Args, " "); got != want || cmd.Stderr || !slices.Contains(cmd.Env, "NEXTEST_EXPERIMENTAL_LIBTEST_JSON=1") {
		t.Errorf("nextest = %q\nwant      %q", got, want)
	}
}

// TestLLVMCoverage reads cargo llvm-cov's JSON export, and agrees with its own line summary.
//
// ref: cargo-llvm-cov/README.md
// quote: "Run all tests regardless of failure and generate report"
func TestLLVMCoverage(t *testing.T) {
	root := fixtureRoot(t, "cargo/basic")
	raw := strings.ReplaceAll(string(loadRecording(t, "cargo-cover").cover), "$ROOT", root)
	cov, err := (&Cargo{}).Coverage(Workspace{Root: root}, strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Data []struct {
			Files []struct {
				Filename string
				Summary  struct{ Lines struct{ Count, Covered int } }
			}
		}
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	for _, f := range doc.Data[0].Files {
		lines := cov.Files[f.Filename]
		covered := 0
		for _, n := range lines {
			if n > 0 {
				covered++
			}
		}
		if len(lines) != f.Summary.Lines.Count || covered != f.Summary.Lines.Covered {
			t.Errorf("%s: %d lines, %d covered; llvm-cov says %d, %d", f.Filename, len(lines), covered, f.Summary.Lines.Count, f.Summary.Lines.Covered)
		}
	}
	lib := cov.Files[filepath.Join(root, "src", "lib.rs")]
	if lib[1] == 0 || lib[36] != 0 { // add's body ran; ignored's never did
		t.Errorf("lib.rs: line 2 = %d, line 37 = %d", lib[1], lib[36])
	}
}
