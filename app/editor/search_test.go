package editor_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/editor"
	"github.com/liamg/oriel/registry"
)

// Project search: the rail's Search opens the panel, a PCRE2 query streams its matches grouped by file, and
// generated files are left out unless the query starts "@all ".
func TestProjectSearch(t *testing.T) {
	s, _ := searchScene(t)
	s.Frame()
	s.ClickOn("Search")
	s.Frame()
	if !s.Showing(editor.SearchView) {
		t.Fatal("the rail's Search didn't open the search panel")
	}
	s.TypeInto("Find", `(?<=return )Round\(`)
	text := waitSearch(s, t, "2 in 2 files")
	full := s.FullText() // a line longer than the panel is cut to fit
	for _, want := range []string{"internal/pricing/round.go", "internal/pricing/tiers.go", "5", "func RoundMinor(v int) int { return ", "Round(", "v) / 100 }"} {
		if !strings.Contains(full, want) {
			t.Errorf("results lack %q:\n%s", want, full)
		}
	}
	if strings.Contains(text, "gen/api.go") {
		t.Error("a generated file was searched")
	}
	s.Run("search.open", registry.Args{"query": "@all Round"})
	text = waitSearch(s, t, "gen/api.go")
	if !strings.Contains(text, "in 3 files") {
		t.Errorf("@all didn't search every file:\n%s", text)
	}
}

// Enter on a match opens its file at its line.
func TestSearchOpensMatches(t *testing.T) {
	s, dir := searchScene(t)
	s.Run("search.open", registry.Args{"query": "tier\\("})
	waitSearch(s, t, "1 in 1 file")
	s.ClickText("tier(")
	s.Frame()
	p := editorOf(s).Current()
	if filepath.Join(dir, p.Name()) != filepath.Join(dir, "internal/pricing/tiers.go") || p.Cursor().Line != 2 {
		t.Errorf("opened %s at line %d", p.Name(), p.Cursor().Line+1)
	}
}

// Replace shows a diff before applying: Replace all… opens the diff of every file it would change and
// writes nothing; Apply writes the files, and an open buffer is changed in the buffer, as one undoable step.
func TestReplacePreviewThenApply(t *testing.T) {
	s, dir := searchScene(t)
	s.Run("editor.open", registry.Args{"path": filepath.Join(dir, "internal/pricing/tiers.go")})
	s.Run("search.open", nil)
	s.Frame()
	s.Frame()
	s.TypeInto("Find", `Round(\w*)\(`)
	s.TypeInto("Replace", `Rounded$1(`)
	waitSearch(s, t, "4 in 2 files")
	if !strings.Contains(s.Text(), "RoundedMinor(") {
		t.Errorf("the results don't preview the replacement:\n%s", s.Text())
	}
	s.Click(apptest.Centre(s.Node("Replace all…").Bounds), 0)
	s.Until("Replace all… to show the diff", func() bool { return s.Showing(editor.ReplaceView) })
	s.Frame()
	text := s.Text()
	for _, want := range []string{"4 replacements in 2 files", "- func Round(v int) int { return v * 100 }", "+ func Rounded(v int) int { return v * 100 }",
		"- func tier() int { return Round(3) }", "+ func tier() int { return Rounded(3) }"} {
		if !strings.Contains(text, want) {
			t.Errorf("the diff lacks %q:\n%s", want, text)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "internal/pricing/round.go")); strings.Contains(string(b), "Rounded") {
		t.Fatal("the preview wrote a file")
	}
	s.Click(apptest.Centre(s.Node("Apply").Bounds), 0)
	s.Frame()
	b, _ := os.ReadFile(filepath.Join(dir, "internal/pricing/round.go"))
	if want := "package pricing\n\nfunc Rounded(v int) int { return v * 100 }\n\nfunc RoundedMinor(v int) int { return Rounded(v) / 100 }\n"; string(b) != want {
		t.Errorf("round.go after Apply:\n%s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "internal/pricing/tiers.go")); strings.Contains(string(b), "Rounded") {
		t.Error("the open tiers.go was written on disk rather than changed in its buffer")
	}
	s.Run("editor.open", registry.Args{"path": filepath.Join(dir, "internal/pricing/tiers.go")})
	s.Frame()
	if got := s.Line(2); got != "func tier() int { return Rounded(3) }" {
		t.Errorf("tiers.go's buffer: %q", got)
	}
	keys(s, "u")
	if got := s.Line(2); got != "func tier() int { return Round(3) }" {
		t.Errorf("u didn't undo the replace: %q", got)
	}
	if s.Showing(editor.ReplaceView) {
		t.Error("the diff stayed open after Apply")
	}
}

// A key layer's keys win while it's active; keys it doesn't map, and every key while it isn't, go on as before.
func TestKeyLayers(t *testing.T) {
	s, _ := searchScene(t)
	ran := 0
	on := false
	s.Registry().Command(registry.Command{ID: "test.layer.x", Run: func(registry.Ctx, registry.Args) error { ran++; return nil }})
	s.Registry().Keymap(registry.Keymap{Mode: "test.layer", Keys: "x", Command: "test.layer.x"})
	s.Registry().Contribute(registry.KeyLayer{Name: "test", Mode: "test.layer", Active: func() bool { return on }})
	if modes := s.KeyModes(); slices.Contains(modes, "test.layer") {
		t.Fatalf("an inactive layer is in %v", modes)
	}
	on = true
	if modes := s.KeyModes(); len(modes) == 0 || modes[0] != "test.layer" {
		t.Fatalf("the active layer isn't first in %v", modes)
	}
	s.Press("x")
	if ran != 1 {
		t.Errorf("x ran the layer's command %d times", ran)
	}
}
