package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// packArchive is a grammar pack as CI publishes it, <name>/ in a .tar.gz, and the checksum list naming its files.
func packArchive(t *testing.T, name string, files map[string][]byte) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	var sums strings.Builder
	for _, f := range slices.Sorted(maps.Keys(files)) {
		tw.WriteHeader(&tar.Header{Name: name + "/" + f, Mode: 0o644, Size: int64(len(files[f])), Typeflag: tar.TypeReg})
		tw.Write(files[f])
		s := sha256.Sum256(files[f])
		fmt.Fprintf(&sums, "%x  %s/%s\n", s, name, f)
	}
	tw.Close()
	zw.Close()
	path := filepath.Join(t.TempDir(), name+".tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, []byte(sums.String())
}

// PL-3: "only a pack that matches [the checksum list] installs", into ~/.config/oriel/languages/<name>/, where ED-11
// loads it from; it's used for its files at once, without asking for consent.
func TestGrammarPackInstall(t *testing.T) {
	g, _ := syntax.Builtin().Language("go")
	wasm, err := g.Grammar()
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"language.toml": []byte("name = \"gox\"\nextensions = [\".gox\"]\n"), "grammar.wasm": wasm,
		"queries/highlights.scm": []byte("(package_identifier) @pack.package\n")}
	path, list := packArchive(t, "gox", files)
	sums, err := syntax.ParsePackSums(list)
	if err != nil {
		t.Fatal(err)
	}
	s := newScene(t, "", t.TempDir(), render.Point{X: 800, Y: 600})
	dir := filepath.Join(s.sh.cfg.ConfigDir, "languages")
	x := &languagesExt{dir: dir, sums: sums}
	if err := x.Activate(context.Background(), s.sh.host("builtin:languages")); err != nil {
		t.Fatal(err)
	}
	if _, ok := languageFor(s.reg, "y.gox", nil); ok {
		t.Fatal("gox is known before its pack is installed")
	}
	s.run("languages.install", registry.Args{"path": path})
	for f, want := range files {
		if got, err := os.ReadFile(filepath.Join(dir, "gox", filepath.FromSlash(f))); err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s in the languages directory: %v", f, err)
		}
	}
	s.frame()
	if len(s.consent().queue) != 0 {
		t.Error("a grammar pack asked for consent")
	}
	if got := captures(t, s.reg, "y.gox"); !slices.Equal(got, []string{"pack.package"}) {
		t.Errorf("y.gox after installing the pack: %v", got)
	}

	// A pack that doesn't match the list installs nothing, and leaves the installed one as it was.
	tampered := maps.Clone(files)
	tampered["queries/highlights.scm"] = []byte("(identifier) @evil\n")
	bad, _ := packArchive(t, "gox", tampered)
	if err := s.reg.Run(context.Background(), "languages.install", registry.Args{"path": bad}); !errors.Is(err, syntax.ErrPackMismatch) {
		t.Errorf("a tampered pack: %v", err)
	}
	if got := captures(t, s.reg, "y.gox"); !slices.Equal(got, []string{"pack.package"}) {
		t.Errorf("y.gox after a refused pack: %v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "gox", "queries", "highlights.scm")); !bytes.Equal(b, files["queries/highlights.scm"]) {
		t.Errorf("a refused pack changed the installed one: %q", b)
	}

	// Installed, it's there at the next start (ED-11).
	s2 := newScene(t, "", t.TempDir(), render.Point{X: 800, Y: 600})
	if err := (&languagesExt{dir: dir}).Activate(context.Background(), s2.sh.host("builtin:languages")); err != nil {
		t.Fatal(err)
	}
	if got := captures(t, s2.reg, "y.gox"); !slices.Equal(got, []string{"pack.package"}) {
		t.Errorf("y.gox at the next start: %v", got)
	}
}

// The binary's list is what languages.install checks: a pack it doesn't list is refused.
func TestGrammarPackNotListed(t *testing.T) {
	path, _ := packArchive(t, "gox", map[string][]byte{"grammar.wasm": []byte("x")})
	s := newScene(t, "", t.TempDir(), render.Point{X: 800, Y: 600})
	if err := s.reg.Run(context.Background(), "languages.install", registry.Args{"path": path}); !errors.Is(err, syntax.ErrPackMismatch) {
		t.Errorf("a pack the binary doesn't list: %v", err)
	}
}
