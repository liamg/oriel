package ratchet

import (
	"go/ast"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/tools/layout"
)

// ceilings may only fall: when a count drops, lower its ceiling to match (AGENTS.md, "Ratchets").
var ceilings = map[string]int{
	"core.Kind* compared outside its kind": 0,
	"kind data read outside its kind":      0,
	"*shell outside app/shell":             0,
	"cfg.Store":                            0,
	"cfg.Host":                             0,
	"functions over 120 lines in app/":     0,
	"files over 900 lines in app/":         0,
	"functions over 120 lines in host/":    0,
	"functions over 120 lines elsewhere":   35,
	"files over 900 lines in host/":        0,
	"Focus calls outside ui/":              0,
	"RequestFocus calls outside ui/":       36,
	"multi-line Input outside Prose":       0,
	"mutable package-level vars":           0,
	// The rest of the tree (cmd, sandbox, render, regex, integ…) is held to the same rule from here, its count only
	// falling: what's truly process-wide (a cgo callback's map, an init-time registry) goes in stateAllowed with why.
	"mutable package-level vars elsewhere": 104,
	"tests writing package-level vars":     0,
	// A test that sleeps guesses how long work takes, and a busy machine makes it wrong: tests wait for the work
	// (apptest's Quiet, a Busy or Wait) or move a clock of their own (internal/clock/clocktest).
	"time.Sleep in tests": 27,
	// Work started with a bare go statement is counted by nothing, so a test can't wait for it: plugin.Go, a
	// plugin.Load, work.Busy or work.Work (whose Loop is for what runs as long as the plugin) count it.
	"go statements in app/": 6,
	// What only a wall clock can measure runs behind the perf build tag (make perf), never in go test ./...
	"wall-clock measures in tests": 4,
	// A timer in a test either guards a hang or guesses how long work takes; a test waits for the work instead.
	"timers in tests": 82,
	// A test frames and runs the loop's queue through apptest's waits (Until, Quiet), not a loop of its own.
	"RunQueued in tests outside apptest": 2,
}

// stateAllowed are the package-level vars MutableVars may pass over, by "dir.name", and why each is safe to share.
var stateAllowed = map[string]string{
	"plugin.native":         "linked-in native plugins, registered from their init (oriel build --with)",
	"ui/widget.fadeIn":      "a transition read, never written",
	"ui/widget.tickIn":      "a transition read, never written",
	"ui/widget.fade":        "a transition read, never written",
	"ui/widget.dropIn":      "a transition read, never written",
	"ui/widget.riseIn":      "a transition read, never written",
	"ui/widget.slideIn":     "a transition read, never written",
	"ui/widget.platform":    "the platform's editing modifiers, from GOOS",
	"core.dataTypes":        "kinds' data types, registered from init",
	"core.eventsMu":         "guards the event types, registered from init",
	"core.eventTypes":       "event types, registered from init",
	"core.eventNames":       "event types, registered from init",
	"agent/packs.builtin":   "the embedded packs, parsed at init",
	"agent/packs.sources":   "the embedded packs, parsed at init",
	"agent/prompts.builtin": "the embedded prompts, parsed at init",
	"text.boundaryCache":    "a test's memo of a pure function",
}

// handWritten are files less those generated.
func handWritten(files []File) []File {
	var out []File
	for _, f := range files {
		if !ast.IsGenerated(f.AST) {
			out = append(out, f)
		}
	}
	return out
}

// tests are files' tests alone.
func tests(files []File) []File {
	var out []File
	for _, f := range files {
		if strings.HasSuffix(f.Path, "_test.go") {
			out = append(out, f)
		}
	}
	return out
}

func repoRoot(t *testing.T) string {
	wd, _ := os.Getwd()
	r, err := layout.Root(wd)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func without(files []File, prefixes ...string) []File {
	var out []File
	for _, f := range files {
		keep := true
		for _, p := range prefixes {
			if strings.HasPrefix(f.Path, p) {
				keep = false
			}
		}
		if keep {
			out = append(out, f)
		}
	}
	return out
}

func TestRatchets(t *testing.T) {
	root := repoRoot(t)
	app, err := Parse(root, "app")
	if err != nil {
		t.Fatal(err)
	}
	host, err := Parse(root, "host")
	if err != nil {
		t.Fatal(err)
	}
	all, err := Parse(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	all = without(all, "tools/", "spec/")
	notUI := without(all, "ui/")
	productDirs := []string{"app", "plugin", "ui", "gitx", "core", "registry", "host", "agent"}
	product, err := ParseAll(root, productDirs...)
	if err != nil {
		t.Fatal(err)
	}
	rest, err := ParseAll(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	rest = without(rest, "tools/", "spec/", "vendor/", "third_party/", ".claude/", ".git/")
	for _, d := range productDirs {
		rest = without(rest, d+"/")
	}
	withTests, err := ParseAll(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	withTests = without(withTests, "tools/", "spec/", "vendor/", "internal/testhook/")
	counts := map[string][]Hit{
		"core.Kind* compared outside its kind": KindCompares(without(all, "core/")),
		"kind data read outside its kind":      KindStandIns(without(all, "core/")),
		"*shell outside app/shell":             TypeRefs(without(app, "app/shell/"), "shell"),
		"cfg.Store":                            Selectors(app, "cfg.Store"),
		"cfg.Host":                             Selectors(app, "cfg.Host"),
		"functions over 120 lines in app/":     LongFuncs(app, 120),
		"files over 900 lines in app/":         LongFiles(app, 900),
		"functions over 120 lines in host/":    LongFuncs(host, 120),
		"files over 900 lines in host/":        LongFiles(host, 900),
		"functions over 120 lines elsewhere":   LongFuncs(handWritten(without(all, "app/", "host/", "third_party/")), 120),
		"Focus calls outside ui/":              UICalls(notUI, "Focus", "FocusInto"),
		"RequestFocus calls outside ui/":       UICalls(notUI, "RequestFocus"),
		"multi-line Input outside Prose":       Multiline(notUI),
		"mutable package-level vars":           MutableVars(product, layout.Module, stateAllowed),
		"mutable package-level vars elsewhere": MutableVars(rest, layout.Module, stateAllowed),
		"tests writing package-level vars":     allowedOut(TestVarWrites(withTests, layout.Module), stateAllowed),
		"time.Sleep in tests":                  PkgSelectors(tests(withTests), "time", "Sleep"),
		"go statements in app/":                GoStmts(without(app, "app/apptest/")),
		"wall-clock measures in tests":         wallClock(Untagged(tests(withTests), "perf")),
		"timers in tests":                      AnyPkgSelectors(Untagged(tests(withTests), "perf"), "time", "After", "Tick", "NewTimer", "NewTicker", "AfterFunc"),
		"RunQueued in tests outside apptest":   Calls(without(Untagged(tests(withTests), "perf"), "app/apptest/"), "RunQueued"),
	}
	for name, hits := range counts {
		ceiling, ok := ceilings[name]
		if !ok {
			t.Errorf("%s: no ceiling", name)
			continue
		}
		switch n := len(hits); {
		case n > ceiling:
			for _, h := range hits {
				t.Logf("  %s: %s", h.Pos, h.What)
			}
			t.Errorf("%s: %d, over its ceiling of %d", name, n, ceiling)
		case n < ceiling:
			t.Logf("%s: %d, under its ceiling of %d: lower the ceiling", name, n, ceiling)
		}
	}
}

// A session's kind-specific data is its kind's (core.KindData), not fields every session carries, which would stand in
// for its kind: se.PR != "".
func TestNoKindFields(t *testing.T) {
	st := reflect.TypeFor[core.SessionState]()
	for _, name := range []string{"PR", "PRRef", "Job", "Review", "Unplanned"} {
		if _, ok := st.FieldByName(name); ok {
			t.Errorf("core.SessionState has %s: it belongs in its kind's data", name)
		}
	}
}

// wallClock finds what measures the machine rather than the code: elapsed time and the process's allocations.
func wallClock(files []File) []Hit {
	return append(PkgSelectors(files, "time", "Since"), append(PkgSelectors(files, "runtime", "ReadMemStats"), PkgSelectors(files, "testing", "AllocsPerRun")...)...)
}

// allowedOut drops the hits on vars allowed.
func allowedOut(hits []Hit, allowed map[string]string) []Hit {
	var out []Hit
	for _, h := range hits {
		if allowed[h.What] == "" {
			out = append(out, h)
		}
	}
	return out
}
