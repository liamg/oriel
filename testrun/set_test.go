package testrun

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
)

// rspec is a plugin's adapter, for a language Oriel doesn't know: it finds `it "…"` lines itself.
type rspec struct{ name string }

func (r rspec) Name() string { return r.name }

func (rspec) Detect(ws Workspace) bool { return ws.Exists("Gemfile") }

func (rspec) Discover(src Source) []Item {
	var out []Item
	for i, l := range strings.Split(string(src.Text.Bytes()), "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSpace(l), `it "`); ok {
			name = strings.TrimSuffix(name, `" do`)
			out = append(out, Item{Package: src.Path, Name: name, Path: []string{name}, File: src.Path, Line: i, Kind: KindTest})
		}
	}
	return out
}

func (rspec) Command(ws Workspace, items []Item, mode Mode) (Cmd, error) {
	return Cmd{Args: []string{"rspec", "--format", "json"}, Dir: ws.Root}, nil
}

func (rspec) Parse(r io.Reader, emit func(Event)) error { return nil }

type otherContribution struct{}

func (otherContribution) ContributionKind() string { return "hud.indicator" }
func (otherContribution) ContributionName() string { return "x" }

func TestSetPluginAdapters(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	os.WriteFile(filepath.Join(ws.Root, "Gemfile"), nil, 0o644)
	spec := filepath.Join(ws.Root, "calc_spec.rb")
	src := []byte("describe Calc do\n  it \"adds\" do\n  end\nend\n")
	s := Builtin()
	type contribution interface {
		ContributionKind() string
		ContributionName() string
	}
	remove := Contribute(s, []contribution{otherContribution{}, Contribution{Adapter: rspec{"rspec"}}})
	a, items := s.Discover(ws, Source{Path: spec, Text: text.New(src, text.Author{}).Snapshot()})
	if a == nil || a.Name() != "rspec" || len(items) != 1 || items[0].Name != "adds" || items[0].Line != 1 {
		t.Fatalf("discovered %v %+v", a, items)
	}
	if (Contribution{Adapter: rspec{"rspec"}}).ContributionKind() != "testrun.adapter" {
		t.Error("contribution kind")
	}
	// A later one of the same name shadows it until it's removed.
	shadow := s.Register(rspec{"rspec"})
	if n := len(s.Adapters()); n != 6 {
		t.Errorf("%d adapters, want the five built-ins and rspec", n)
	}
	shadow()
	remove()
	if _, ok := s.Adapter("rspec"); ok {
		t.Error("rspec is still registered")
	}
}

func TestSetDetectsEachFixture(t *testing.T) {
	s := Builtin()
	for fixture, want := range map[string]string{
		"go/basic":     "go",
		"python/basic": "pytest",
		"vitest/basic": "vitest",
		"jest/basic":   "jest",
		"cargo/basic":  "cargo",
	} {
		var got []string
		for _, a := range s.Detect(Workspace{Root: fixtureRoot(t, fixture)}) {
			got = append(got, a.Name())
		}
		if !slices.Equal(got, []string{want}) {
			t.Errorf("%s: detected %q, want %s", fixture, got, want)
		}
	}
}

// TestDiscoverThroughADocument finds tests with the "tests" query run on the buffer's own syntax document, kept
// up to date as it's edited.
func TestDiscoverThroughADocument(t *testing.T) {
	path, _ := filepath.Abs("testdata/go/basic/calc/calc_test.go")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	buf := text.New(b, text.Author{})
	lang, _ := syntax.Builtin().Language("go")
	doc, err := syntax.NewDocument(lang, buf.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	end := buf.Snapshot().Len()
	ch, err := buf.Apply(text.Edit{Ranges: []text.Replace{{Range: text.Range{Start: end, End: end}, Text: []byte("\nfunc TestAdded(t *testing.T) {\n\tt.Run(\"new case\", func(t *testing.T) {})\n}\n")}}})
	if err != nil {
		t.Fatal(err)
	}
	doc.Change(ch)
	items := (&Go{}).Discover(Source{Path: path, Text: ch.After, Syntax: doc})
	it := itemNamed(t, items, "TestAdded/new_case")
	if it.Kind != KindSubtest || it.Line != ch.After.Lines()-3 {
		t.Errorf("TestAdded/new_case = %+v, want line %d", it, ch.After.Lines()-3)
	}
}

func TestSetAffected(t *testing.T) {
	s := Builtin()
	root := fixtureRoot(t, "python/basic")
	got, err := s.Affected(Workspace{Root: root}, filepath.Join(root, "calc.py"))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for a, items := range got {
		if a.Name() != "pytest" {
			t.Errorf("%s answered for a python project", a.Name())
		}
		for _, it := range items {
			files = append(files, filepath.Base(it.File))
		}
	}
	slices.Sort(files)
	if !slices.Equal(files, []string{"test_calc.py"}) {
		t.Errorf("saving calc.py selects %q", files)
	}
}

// go help test: "the file pattern \"*_test.go\"". pytest goodpractices: "search for “test_*.py“ or “*_test.py“
// files". vitest config: "**Default:** `['**/*.{test,spec}.?(c|m)[jt]s?(x)']`". A plugin's adapter that can tell
// one counts too.
func TestSetTestFile(t *testing.T) {
	s := Builtin()
	s.Register(rspecFiles{rspec{name: "rspec"}})
	for path, want := range map[string]bool{
		"/r/round_test.go":          true,
		"/r/round.go":               false,
		"/r/tests/test_price.py":    true,
		"/r/price_test.py":          true,
		"/r/price.py":               false,
		"/r/src/usage.test.ts":      true,
		"/r/src/usage.spec.mjs":     true,
		"/r/src/usage.ts":           false,
		"/r/spec/billing_spec.rb":   true,
		"/r/lib/billing.rb":         false,
		"/r/internal/testdata/a.go": false,
	} {
		if got := s.TestFile(path); got != want {
			t.Errorf("TestFile(%s) = %v, want %v", path, got, want)
		}
	}
}

type rspecFiles struct{ rspec }

func (rspecFiles) TestFile(path string) bool { return strings.HasSuffix(path, "_spec.rb") }
