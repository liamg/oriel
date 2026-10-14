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
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	a := Fingerprint(dir)
	if a == "" || Fingerprint(dir) != a {
		t.Fatalf("not stable: %q", a)
	}
	os.WriteFile(filepath.Join(dir, ".git", "index"), []byte("x"), 0o644)
	if Fingerprint(dir) != a {
		t.Error("a dot-directory changed it")
	}
	later := time.Now().Add(time.Minute)
	os.Chtimes(filepath.Join(dir, "a.go"), later, later)
	if Fingerprint(dir) == a {
		t.Error("touching a file didn't change it")
	}
}
