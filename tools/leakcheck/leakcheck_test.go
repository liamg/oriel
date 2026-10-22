package leakcheck

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestTerms(t *testing.T) {
	got := Terms("# names\nAcme Corp\n\n  /Users/someone  \n")
	if want := []string{"acme corp", "/users/someone"}; !slices.Equal(got, want) {
		t.Errorf("Terms = %q, want %q", got, want)
	}
}

func TestScanFindsTermsAnyCase(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("ok\nsee ACME corp docs\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.png"), []byte("\x00acme corp"), 0o644)
	os.WriteFile(filepath.Join(dir, "c.md"), []byte("path ~/.crit/work/x\n"), 0o644)
	hits, err := Scan(dir, []string{"a.go", "b.png", "c.md", "gone.txt"}, append(Terms("acme corp"), Builtin...))
	if err != nil {
		t.Fatal(err)
	}
	want := []Hit{{File: "a.go", Line: 2, Term: "acme corp"}, {File: "c.md", Line: 1, Term: ".crit/work"}}
	if !slices.Equal(hits, want) {
		t.Errorf("hits = %v, want %v", hits, want)
	}
}
