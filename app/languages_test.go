package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/syntax"
)

const goSrc = "package x\n\nfunc f() int { return 1 }\n"

func captures(t *testing.T, r registry.Registry, path string) []string {
	t.Helper()
	l, ok := languageFor(r, path, []byte(goSrc))
	if !ok {
		t.Fatalf("no language for %s", path)
	}
	p, err := syntax.NewParser(l)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.Parse([]byte(goSrc))
	spans, err := p.Highlights(0, len(goSrc))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range spans {
		if !slices.Contains(out, s.Capture) {
			out = append(out, s.Capture)
		}
	}
	slices.Sort(out)
	return out
}

// ED-11: "A test registers a language through the registry and gets highlights for a fixture; disposing it restores
// the built-in one it shadowed."
func TestLanguageContribution(t *testing.T) {
	s := newScene(t, "", t.TempDir(), render.Point{X: 800, Y: 600})
	builtin := captures(t, s.reg, "x.go")
	if !slices.Contains(builtin, "keyword.function") {
		t.Fatalf("the built-in go highlights %v", builtin)
	}
	g, _ := syntax.Builtin().Language("go")
	d := s.reg.Language(registry.Language{Name: "go", Extensions: []string{".go"}, Grammar: g.Grammar, Source: "test",
		Queries: map[string]string{"highlights": "(identifier) @custom.ident"}})
	if got := captures(t, s.reg, "x.go"); !slices.Equal(got, []string{"custom.ident"}) {
		t.Errorf("with the shadowing language: %v", got)
	}
	d.Dispose()
	if got := captures(t, s.reg, "x.go"); !slices.Equal(got, builtin) {
		t.Errorf("after disposing it: %v, want %v", got, builtin)
	}
}

// ED-11: "A grammar pack dropped into the languages directory is used for its files, and wins over a built-in of
// the same name."
func TestLanguagePack(t *testing.T) {
	g, _ := syntax.Builtin().Language("go")
	wasm, err := g.Grammar()
	if err != nil {
		t.Fatal(err)
	}
	conf := t.TempDir()
	for _, pack := range []struct{ dir, manifest, query string }{
		{"go", "", "(identifier) @pack.ident\n"},
		{"gox", "name = \"gox\"\nextensions = [\".gox\"]\n", "(package_identifier) @pack.package\n"},
	} {
		dir := filepath.Join(conf, "languages", pack.dir)
		os.MkdirAll(filepath.Join(dir, "queries"), 0o755)
		os.WriteFile(filepath.Join(dir, "grammar.wasm"), wasm, 0o644)
		os.WriteFile(filepath.Join(dir, "queries", "highlights.scm"), []byte(pack.query), 0o644)
		if pack.manifest != "" {
			os.WriteFile(filepath.Join(dir, "language.toml"), []byte(pack.manifest), 0o644)
		} else {
			os.WriteFile(filepath.Join(dir, "language.toml"), []byte("extensions = [\".go\"]\n"), 0o644)
		}
	}
	s := newScene(t, "", t.TempDir(), render.Point{X: 800, Y: 600})
	// builtin:languages as it starts with packs in its directory.
	if err := (&languagesExt{dir: filepath.Join(conf, "languages")}).Activate(context.Background(), s.sh.host("builtin:languages")); err != nil {
		t.Fatal(err)
	}
	if got := captures(t, s.reg, "x.go"); !slices.Equal(got, []string{"pack.ident"}) {
		t.Errorf("x.go highlights with the pack's queries? %v", got)
	}
	if got := captures(t, s.reg, "y.gox"); !slices.Equal(got, []string{"pack.package"}) {
		t.Errorf("y.gox: %v", got)
	}
}
