package testrun

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Recordings are real runner output, captured from the fixtures under testdata/<lang>/ and replayed through each
// adapter's Parse. To capture them again, put the pinned tools on PATH (see testdata/recorded/README) and run
//
//	go test ./testrun -run TestRecord -record
//
// then rewrite the expected events with -update and review the diff.
var (
	record = flag.Bool("record", false, "run the real test runners on the fixtures and rewrite testdata/recorded")
	update = flag.Bool("update", false, "rewrite the expected event goldens from the recordings")
)

type recording struct {
	name     string
	fixture  string // under testdata
	adapter  func() Adapter
	items    func(root string) []Item // nil runs everything
	mode     Mode
	env      []string
	timeout  time.Duration // the runner is killed after this long
	versions [][]string    // commands that name the tools' versions
	setup    func(t *testing.T, root string)
	args     func(root string) []string // a command of its own instead of the adapter's, for an oracle
}

// recordTool is a tool to record with: from ORIEL_RECORD_<NAME> when set, else the name on PATH.
func recordTool(name string) string {
	if v := os.Getenv("ORIEL_RECORD_" + strings.ToUpper(name)); v != "" {
		return v
	}
	return name
}

var python = recordTool("python3")

var recordings = []recording{
	{
		name: "go-basic", fixture: "go/basic", mode: ModeRun, env: []string{"GOWORK=off", "GOFLAGS=-count=1"},
		adapter:  func() Adapter { return &Go{Flags: []string{"-timeout=3s"}} },
		versions: [][]string{{"go", "version"}},
	},
	{
		name: "go-selected", fixture: "go/basic", mode: ModeRun, env: []string{"GOWORK=off", "GOFLAGS=-count=1"},
		adapter: func() Adapter { return &Go{} },
		items: func(root string) []Item {
			f := filepath.Join(root, "calc", "calc_test.go")
			return []Item{{Package: "example.com/basic/calc", Name: "TestNested/outer/inner_pass", File: f, Kind: KindSubtest}}
		},
		versions: [][]string{{"go", "version"}},
	},
	{
		name: "go-cover", fixture: "go/watch", mode: ModeCoverage, env: []string{"GOWORK=off", "GOFLAGS=-count=1"},
		adapter:  func() Adapter { return &Go{} },
		versions: [][]string{{"go", "version"}},
	},
	{
		name: "pytest-basic", fixture: "python/basic", mode: ModeRun, timeout: 20 * time.Second,
		adapter: func() Adapter { return &Pytest{Python: python} },
		items: func(root string) []Item {
			return []Item{
				{Package: "tests/test_calc.py", File: filepath.Join(root, "tests", "test_calc.py"), Kind: KindFile},
				{Package: "tests/test_zslow.py", File: filepath.Join(root, "tests", "test_zslow.py"), Kind: KindFile},
			}
		},
		versions: pythonVersions,
	},
	{
		name: "pytest-broken", fixture: "python/basic", mode: ModeRun,
		adapter: func() Adapter { return &Pytest{Python: python} },
		items: func(root string) []Item {
			return []Item{{Package: "tests/test_broken_import.py", File: filepath.Join(root, "tests", "test_broken_import.py"), Kind: KindFile}}
		},
		versions: pythonVersions,
	},
	{
		name: "pytest-collect", fixture: "python/basic",
		adapter:  func() Adapter { return &Pytest{Python: python} },
		args:     func(string) []string { return []string{python, "-m", "pytest", "--collect-only", "-q"} },
		versions: pythonVersions,
	},
	{
		name: "pytest-cover", fixture: "python/basic", mode: ModeCoverage,
		adapter: func() Adapter { return &Pytest{Python: python} },
		items: func(root string) []Item {
			return []Item{{Package: "tests/test_calc.py", Name: "test_pass", File: filepath.Join(root, "tests", "test_calc.py"), Kind: KindTest}}
		},
		versions: pythonVersions,
	},
	cargoRecording("cargo-test", false, ModeRun, nil),
	cargoRecording("cargo-test-selected", false, ModeRun, cargoSelected),
	cargoRecording("cargo-nextest", true, ModeRun, nil),
	cargoRecording("cargo-nextest-selected", true, ModeRun, cargoSelected),
	cargoRecording("cargo-cover", false, ModeCoverage, func(string) []Item { return []Item{{Package: "lib", Kind: KindPackage}} }),
	jsRecording("vitest-basic", "vitest", ModeRun, func() Adapter { return &Vitest{} }, nil),
	jsRecording("vitest-selected", "vitest", ModeRun, func() Adapter { return &Vitest{} }, jsSelected("vitest", "test/math.test.ts")),
	jsRecording("vitest-related", "vitest", ModeWatch, func() Adapter { return &Vitest{} }, func(root string) []Item {
		f := filepath.Join(root, "src", "math.ts")
		return []Item{{Package: f, File: f, Kind: KindFile}}
	}),
	jsRecording("vitest-cover", "vitest", ModeCoverage, func() Adapter { return &Vitest{} }, nil),
	jsRecording("jest-basic", "jest", ModeRun, func() Adapter { return &Jest{} }, nil),
	jsRecording("jest-selected", "jest", ModeRun, func() Adapter { return &Jest{} }, jsSelected("jest", "math.test.js")),
	jsRecording("jest-cover", "jest", ModeCoverage, func() Adapter { return &Jest{} }, nil),
}

func cargoRecording(name string, nextest bool, mode Mode, items func(string) []Item) recording {
	versions := [][]string{{"cargo", "--version"}, {"rustc", "--version"}}
	if nextest {
		versions = append(versions, []string{"cargo", "nextest", "--version"})
	}
	if mode == ModeCoverage {
		versions = append(versions, []string{"cargo", "llvm-cov", "--version"})
	}
	return recording{
		name: name, fixture: "cargo/basic", mode: mode, items: items, versions: versions, timeout: 75 * time.Second,
		adapter: func() Adapter { return &Cargo{Nextest: &nextest} },
	}
}

// cargoSelected picks a test in a nested module of the library and one in an integration test.
func cargoSelected(root string) []Item {
	return []Item{
		{Package: "lib", Name: "tests::inner::deep", File: filepath.Join(root, "src", "lib.rs"), Kind: KindTest},
		{Package: "test/integration", Name: "adds", File: filepath.Join(root, "tests", "integration.rs"), Kind: KindTest},
	}
}

// jsSelected picks a nested test and an .each row.
func jsSelected(runner, file string) func(string) []Item {
	return func(root string) []Item {
		f := filepath.Join(root, filepath.FromSlash(file))
		return []Item{
			{Package: f, Name: "outer > inner > nested pass", Path: []string{"outer", "inner", "nested pass"}, File: f, Kind: KindTest},
			{Package: f, Name: "add(2, 2) -> 5", Path: []string{"add(2, 2) -> 5"}, File: f, Kind: KindRow},
		}
	}
}

func npmInstall(t *testing.T, root string) {
	if _, err := os.Stat(filepath.Join(root, "node_modules")); err == nil {
		return
	}
	c := exec.Command("npm", "install", "--no-audit", "--no-fund", "--no-package-lock")
	c.Dir = root
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("npm install: %v\n%s", err, out)
	}
}

func jsRecording(name, runner string, mode Mode, adapter func() Adapter, items func(root string) []Item) recording {
	return recording{
		name: name, fixture: runner + "/basic", mode: mode, adapter: adapter, items: items, setup: npmInstall,
		versions: [][]string{{"node", "--version"}, {"npx", "--no-install", runner, "--version"}},
	}
}

var pythonVersions = [][]string{
	{python, "--version"},
	{python, "-m", "pytest", "--version"},
	{python, "-c", "import pytest_cov, coverage; print('pytest-cov', pytest_cov.__version__, 'coverage', coverage.__version__)"},
}

// A recorded run: the header names the command and the tools' versions; the body is the runner's stdout.
type recorded struct {
	header map[string][]string
	body   []byte
	cover  []byte
}

const recPrefix = "#oriel-record "

func loadRecording(t *testing.T, name string) recorded {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "recorded", name+".rec"))
	if err != nil {
		t.Fatalf("no recording %s (record it with -record): %v", name, err)
	}
	rec := recorded{header: map[string][]string{}}
	for bytes.HasPrefix(b, []byte(recPrefix)) {
		line, rest, _ := bytes.Cut(b, []byte("\n"))
		k, v, _ := strings.Cut(strings.TrimPrefix(string(line), recPrefix), ": ")
		rec.header[k] = append(rec.header[k], v)
		b = rest
	}
	rec.body = b
	rec.cover, _ = os.ReadFile(filepath.Join("testdata", "recorded", name+".cover"))
	return rec
}

func fixtureRoot(t *testing.T, fixture string) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestRecord runs the real runners on the fixtures, with -record.
func TestRecord(t *testing.T) {
	if !*record {
		t.Skip("run with -record to capture runner output again")
	}
	for _, rc := range recordings {
		t.Run(rc.name, func(t *testing.T) { recordOne(t, rc) })
	}
}

func recordOne(t *testing.T, rc recording) {
	root := fixtureRoot(t, rc.fixture)
	for _, v := range rc.versions {
		if _, err := exec.LookPath(v[0]); err != nil {
			t.Fatalf("%s isn't on PATH: %v", v[0], err)
		}
	}
	if rc.setup != nil {
		rc.setup(t, root)
	}
	ws := Workspace{Root: root, Env: rc.env}
	var items []Item
	if rc.items != nil {
		items = rc.items(root)
	}
	a := rc.adapter()
	cmd, err := a.Command(ws, items, rc.mode)
	if err != nil {
		t.Fatal(err)
	}
	if rc.args != nil {
		cmd = Cmd{Args: rc.args(root), Dir: root, Env: ws.Env}
	}
	dir := t.TempDir()
	for name, b := range cmd.Files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	expand := func(s string) string { return strings.ReplaceAll(s, RunDir, dir) }
	var args []string
	for _, a := range cmd.Args {
		args = append(args, expand(a))
	}
	timeout := rc.timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Dir = cmd.Dir
	c.Env = append(os.Environ(), "ORIEL_TESTRUN_DIR="+dir)
	for _, e := range cmd.Env {
		c.Env = append(c.Env, expand(e))
	}
	killGroup(c)
	c.WaitDelay = 3 * time.Second
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if cmd.Stderr {
		c.Stderr = &stdout
	}
	err = c.Run()
	exit := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, stderr.String())
	}
	norm := normaliser(root, dir)
	var hdr bytes.Buffer
	fmt.Fprintf(&hdr, "%sargs: %s\n", recPrefix, norm(strings.Join(args, " ")))
	for _, e := range cmd.Env {
		fmt.Fprintf(&hdr, "%senv: %s\n", recPrefix, norm(expand(e)))
	}
	for _, v := range rc.versions {
		vc := exec.Command(v[0], v[1:]...)
		vc.Dir = root
		out, err := vc.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v", v, err)
		}
		fmt.Fprintf(&hdr, "%sversion: %s: %s\n", recPrefix, norm(strings.Join(v, " ")), norm(strings.TrimSpace(strings.ReplaceAll(string(out), "\n", " "))))
	}
	fmt.Fprintf(&hdr, "%splatform: %s/%s\n", recPrefix, runtime.GOOS, runtime.GOARCH)
	if ctx.Err() != nil {
		fmt.Fprintf(&hdr, "%skilled: after %s\n", recPrefix, timeout)
	}
	fmt.Fprintf(&hdr, "%sexit: %d\n", recPrefix, exit)
	out := filepath.Join("testdata", "recorded")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, rc.name+".rec"), append(hdr.Bytes(), norm(stdout.String())...), 0o644); err != nil {
		t.Fatal(err)
	}
	if cmd.CoverFile != "" {
		b, err := os.ReadFile(filepath.Join(dir, cmd.CoverFile))
		if err != nil {
			t.Fatalf("no coverage file: %v\n%s", err, stderr.String())
		}
		if err := os.WriteFile(filepath.Join(out, rc.name+".cover"), []byte(norm(string(b))), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("%s: exit %d, %d bytes\n%s", rc.name, exit, stdout.Len(), stderr.String())
}

// normaliser replaces the paths that differ between machines with names: $ROOT for the fixture, $RUNDIR for the
// run's directory, $TOOLS for ORIEL_RECORD_TOOLS (where the pinned tools are), $GOROOT and $HOME.
func normaliser(root, dir string) func(string) string {
	var pairs []string
	add := func(from, to string) {
		if from != "" && from != "/" {
			pairs = append(pairs, from, to)
		}
	}
	add(dir, "$RUNDIR")
	add(root, "$ROOT")
	add(os.Getenv("ORIEL_RECORD_TOOLS"), "$TOOLS")
	if out, err := exec.Command("go", "env", "GOROOT").Output(); err == nil {
		add(strings.TrimSpace(string(out)), "$GOROOT")
	}
	if out, err := exec.Command("go", "env", "GOMODCACHE").Output(); err == nil {
		add(strings.TrimSpace(string(out)), "$GOMODCACHE")
	}
	home, _ := os.UserHomeDir()
	add(home, "$HOME")
	add(os.TempDir(), "$TMPDIR")
	r := strings.NewReplacer(pairs...)
	return r.Replace
}

// TestRecordedEvents replays each recording through its adapter and compares the events with the expected golden.
func TestRecordedEvents(t *testing.T) {
	for _, rc := range recordings {
		if rc.args != nil {
			continue
		}
		t.Run(rc.name, func(t *testing.T) {
			got := replay(t, rc)
			var b bytes.Buffer
			enc := json.NewEncoder(&b)
			enc.SetEscapeHTML(false)
			for _, e := range got {
				if err := enc.Encode(e); err != nil {
					t.Fatal(err)
				}
			}
			golden := filepath.Join("testdata", "recorded", rc.name+".events")
			if *update {
				if err := os.WriteFile(golden, b.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("no golden (write it with -update): %v", err)
			}
			if !bytes.Equal(b.Bytes(), want) {
				t.Errorf("events differ from %s; diff the output of -update against it", golden)
				diffLines(t, string(want), b.String())
			}
		})
	}
}

func diffLines(t *testing.T, want, got string) {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < max(len(w), len(g)); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			t.Errorf("line %d:\nwant %s\ngot  %s", i+1, a, b)
			return
		}
	}
}

// replay is a recording's events, through its adapter's Parse.
func replay(t *testing.T, rc recording) []Event {
	t.Helper()
	rec := loadRecording(t, rc.name)
	var got []Event
	if err := rc.adapter().Parse(bytes.NewReader(rec.body), func(e Event) { got = append(got, e) }); err != nil {
		t.Fatal(err)
	}
	return got
}

func recordingNamed(t *testing.T, name string) recording {
	t.Helper()
	for _, rc := range recordings {
		if rc.name == name {
			return rc
		}
	}
	t.Fatalf("no recording %s", name)
	return recording{}
}

// finals is the last result event per test key: passed, failed or skipped.
func finals(evs []Event) map[string]Event {
	out := map[string]Event{}
	for _, e := range evs {
		switch e.Kind {
		case Passed, Failed, Skipped, BuildFailed:
			out[e.Key()] = e
		}
	}
	return out
}

func readLines(b []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}
