package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

// Write puts the new content in place at once: a reader sees the old file or the new one, never an empty or
// half-written one, and no temporary file is left beside it. A symlink stays one, its target replaced.
func TestWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.toml")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("[plugins]\n\"acme/hi\" = \"*\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "[plugins]\n\"acme/hi\" = \"*\"\n" {
		t.Errorf("it holds %q", b)
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Errorf("left beside it: %v", es)
	}

	real := filepath.Join(dir, "dotfiles", "settings.toml")
	os.MkdirAll(filepath.Dir(real), 0o755)
	os.WriteFile(real, []byte("old\n"), 0o644)
	link := filepath.Join(dir, "linked.toml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := Write(link, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a file")
	}
	if b, _ := os.ReadFile(real); string(b) != "new\n" {
		t.Errorf("the link's target holds %q", b)
	}

	if err := Write(filepath.Join(dir, "missing", "x.toml"), []byte("x"), 0o644); err == nil {
		t.Error("a file in a directory that isn't there was written")
	}
}

// WriteSecret is Write for what only the user may read: the file is 0600 afterwards, even one that was readable by
// others before, and a new one is never readable by others, not even for a moment.
func TestWriteSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSecret(path, []byte(`{"token":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, %v", fi.Mode().Perm(), err)
	}
	if b, _ := os.ReadFile(path); string(b) != `{"token":"x"}` {
		t.Errorf("it holds %q", b)
	}
	fresh := filepath.Join(dir, "new.json")
	if err := WriteSecret(fresh, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(fresh); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("new file's mode %v, %v", fi.Mode().Perm(), err)
	}
	if es, _ := os.ReadDir(dir); len(es) != 2 {
		t.Errorf("left beside it: %v", es)
	}
}
