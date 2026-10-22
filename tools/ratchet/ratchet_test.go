package ratchet

import (
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
	"files over 900 lines in host/":        0,
	"Focus calls outside ui/":              0,
	"RequestFocus calls outside ui/":       40,
	"multi-line Input outside Prose":       0,
	"mutable package-level vars":           0,
	"tests writing package-level vars":     0,
}

// stateAllowed are the package-level vars MutableVars may pass over, by "dir.name", and why each is safe to share.
var stateAllowed = map[string]string{
	"app/kit.langSets":      "keyed by window registry, and a window's dropped as it closes (ForgetLanguages)",
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
	product, err := ParseAll(root, "app", "plugin", "ui", "gitx", "core", "registry", "host", "agent")
	if err != nil {
		t.Fatal(err)
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
		"Focus calls outside ui/":              UICalls(notUI, "Focus", "FocusInto"),
		"RequestFocus calls outside ui/":       UICalls(notUI, "RequestFocus"),
		"multi-line Input outside Prose":       Multiline(notUI),
		"mutable package-level vars":           MutableVars(product, layout.Module, stateAllowed),
		"tests writing package-level vars":     allowedOut(TestVarWrites(withTests, layout.Module), stateAllowed),
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
