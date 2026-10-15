package syntax

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type packFile struct {
	name, body string
	typ        byte
}

func packArchive(t *testing.T, files ...packFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, f := range files {
		h := &tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body)), Typeflag: f.typ}
		switch f.typ {
		case 0:
			h.Typeflag = tar.TypeReg
		case tar.TypeSymlink:
			h.Linkname, h.Size = f.body, 0
		case tar.TypeDir:
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(f.body))
		}
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func sumsOf(files ...packFile) []byte {
	var b strings.Builder
	for _, f := range files {
		s := sha256.Sum256([]byte(f.body))
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(s[:]), f.name)
	}
	return []byte(b.String())
}

var gox = []packFile{
	{name: "gox/language.toml", body: "name = \"gox\"\nextensions = [\".gox\"]\n"},
	{name: "gox/grammar.wasm", body: "\x00asm grammar"},
	{name: "gox/queries/highlights.scm", body: "(identifier) @variable\n"},
}

func testSums(t *testing.T, files ...packFile) PackSums {
	t.Helper()
	s, err := ParsePackSums(sumsOf(files...))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// PL-3: a pack that matches the checksum list installs into <dir>/<name>, as ED-11's languages directory reads it.
func TestPackInstall(t *testing.T) {
	sums := testSums(t, gox...)
	dir := t.TempDir()
	name, err := sums.Install(bytes.NewReader(packArchive(t, append([]packFile{{name: "gox/", typ: tar.TypeDir}}, gox...)...)), dir)
	if err != nil || name != "gox" {
		t.Fatalf("Install: %q, %v", name, err)
	}
	for _, f := range gox {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.name)))
		if err != nil || string(b) != f.body {
			t.Errorf("%s: %q, %v", f.name, b, err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("the languages directory holds %v, want only gox", entries)
	}

	// Installing again replaces the pack whole: a file the old one had and the new one doesn't goes.
	os.WriteFile(filepath.Join(dir, "gox", "queries", "stale.scm"), []byte("x"), 0o644)
	if _, err := sums.Install(bytes.NewReader(packArchive(t, gox...)), dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "gox", "queries", "stale.scm")); !os.IsNotExist(err) {
		t.Error("a reinstall kept a file the pack doesn't have")
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("after a reinstall the directory holds %v", entries)
	}
}

// PL-3 / deps.md: only a pack that matches the shipped checksum list installs; anything else writes nothing.
func TestPackInstallRefusesMismatches(t *testing.T) {
	sums := testSums(t, gox...)
	tampered := slices.Clone(gox)
	tampered[1].body = "\x00asm something else"
	for _, tc := range []struct {
		what  string
		files []packFile
	}{
		{"a changed grammar", tampered},
		{"a file missing", gox[:2]},
		{"a file not listed", append(slices.Clone(gox), packFile{name: "gox/queries/extra.scm", body: "x"})},
		{"a pack Oriel doesn't know", []packFile{{name: "other/grammar.wasm", body: "x"}}},
		{"a path out of the pack", append(slices.Clone(gox), packFile{name: "gox/../evil", body: "x"})},
		{"a second pack", append(slices.Clone(gox), packFile{name: "dart/grammar.wasm", body: "x"})},
		{"a symlink", append(slices.Clone(gox), packFile{name: "gox/queries/link.scm", body: "/etc/passwd", typ: tar.TypeSymlink})},
		{"an empty archive", nil},
	} {
		t.Run(tc.what, func(t *testing.T) {
			dir := t.TempDir()
			_, err := sums.Install(bytes.NewReader(packArchive(t, tc.files...)), dir)
			if !errors.Is(err, ErrPackMismatch) {
				t.Errorf("err = %v, want ErrPackMismatch", err)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("it wrote %v", entries)
			}
		})
	}
	if _, err := sums.Install(strings.NewReader("not gzip"), t.TempDir()); err == nil {
		t.Error("a file that isn't a .tar.gz installed")
	}
}

// The list the binary ships is build.sh's: every pack has its grammar and language.toml.
func TestShippedPackSums(t *testing.T) {
	names := Packs().Names()
	if len(names) == 0 {
		t.Fatal("no packs in grammars/PACKS.SHA256SUMS")
	}
	for _, n := range names {
		for _, f := range []string{"grammar.wasm", "language.toml", "queries/highlights.scm"} {
			if Packs()[n][f] == "" {
				t.Errorf("pack %s lists no %s", n, f)
			}
		}
		toml, err := os.ReadFile(filepath.Join("grammars", "packs", n+".toml"))
		if err != nil {
			t.Errorf("pack %s: %v", n, err)
			continue
		}
		if s := sha256.Sum256(toml); hex.EncodeToString(s[:]) != Packs()[n]["language.toml"] {
			t.Errorf("grammars/packs/%s.toml doesn't match its checksum: run build.sh", n)
		}
	}
	for _, l := range Builtin().Languages() {
		if Packs()[l.Name] != nil {
			t.Errorf("%s is both built in and a pack", l.Name)
		}
	}
}
