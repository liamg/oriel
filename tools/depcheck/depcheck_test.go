package depcheck

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMajor(t *testing.T) {
	for v, want := range map[string]string{
		"v0.0.0-20240101000000-abcdef": "v0",
		"v1.2.3":                       "v1",
		"v2.0.0+incompatible":          "v2",
		"v10.1.0":                      "v10",
	} {
		if got := (Module{Version: v}).Major(); got != want {
			t.Errorf("Major(%s) = %s, want %s", v, got, want)
		}
	}
}

func TestParseAllowlist(t *testing.T) {
	a, err := ParseAllowlist(strings.NewReader("# comment\n\ngioui.org v0 # Gio\ngolang.org/x/sys v0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !a["gioui.org"]["v0"] || !a["golang.org/x/sys"]["v0"] || len(a) != 2 {
		t.Errorf("got %v", a)
	}
	if _, err := ParseAllowlist(strings.NewReader("gioui.org\n")); err == nil {
		t.Error("want an error for a line with no major version")
	}
	if _, err := ParseAllowlist(strings.NewReader("gioui.org v0.2.0\n")); err == nil {
		t.Error("want an error for a full version instead of a major")
	}
}

func TestRequires(t *testing.T) {
	dir := t.TempDir()
	gomod := filepath.Join(dir, "go.mod")
	os.WriteFile(gomod, []byte(`module example.com/a

go 1.26.0

require gioui.org v0.8.0

require (
	golang.org/x/sys v0.30.0 // indirect
	github.com/liamg/oriel/text v0.0.0
)
`), 0o644)
	got, err := Requires(gomod)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, m := range got {
		paths = append(paths, m.Path+"@"+m.Version)
	}
	want := []string{"gioui.org@v0.8.0", "golang.org/x/sys@v0.30.0", "github.com/liamg/oriel/text@v0.0.0"}
	if !slices.Equal(paths, want) {
		t.Errorf("got %q, want %q", paths, want)
	}
}

func TestVendored(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "modules.txt")
	os.WriteFile(f, []byte("# gioui.org v0.8.0\n## explicit; go 1.21\ngioui.org/app\n# golang.org/x/image v0.20.0\n## explicit\ngolang.org/x/image/math/fixed\n# github.com/liamg/oriel/text\n"), 0o644)
	got, err := Vendored(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != "gioui.org" || got[1].Path != "golang.org/x/image" {
		t.Errorf("got %+v", got)
	}
}

func TestCheck(t *testing.T) {
	allow := Allowlist{"gioui.org": {"v0": true}}
	mods := []Module{
		{"gioui.org", "v0.8.0", "a/go.mod"},
		{"github.com/liamg/oriel/text", "v0.0.0", "a/go.mod"},
		{"github.com/someone/thing", "v1.0.0", "a/go.mod"},
		{"gioui.org", "v2.0.0", "b/go.mod"},
		{"github.com/someone/thing", "v1.1.0", "b/go.mod"},
	}
	got := Check(allow, mods)
	if len(got) != 2 || !strings.HasPrefix(got[0], "gioui.org v2.0.0") || !strings.HasPrefix(got[1], "github.com/someone/thing v1.0.0") {
		t.Errorf("got %q", got)
	}
}

// The allowlist parses and approves everything the repo builds.
func TestAllowedFile(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(root, AllowedFile))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if a, err := ParseAllowlist(f); err != nil || !a["golang.org/x/sys"]["v0"] {
		t.Fatalf("allowed.txt: %v %v", a, err)
	}
	problems, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}
