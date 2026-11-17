package testrun

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A fingerprint stays while nothing changes, moves when a file does, and ignores dot-directories.
func TestFingerprint(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := Fingerprint(dir)
	if a == "" || Fingerprint(dir) != a {
		t.Fatalf("not stable: %q", a)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "index"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if Fingerprint(dir) != a {
		t.Error("a dot-directory changed it")
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(dir, "a.go"), later, later); err != nil {
		t.Fatal(err)
	}
	if Fingerprint(dir) == a {
		t.Error("touching a file didn't change it")
	}
}
