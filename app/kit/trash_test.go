package kit

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A trashed file leaves its place for the trash, under a name not taken there, with what restoring it needs on Linux.
func TestTrash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	files := filepath.Join(home, "data", "Trash", "files")
	if runtime.GOOS == "darwin" {
		files = filepath.Join(home, ".Trash")
	}
	dir := t.TempDir()
	for range 2 {
		p := filepath.Join(dir, "notes.md")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Trash(p); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s is still there", p)
		}
	}
	for _, name := range []string{"notes.md", "notes 2.md"} {
		if _, err := os.Stat(filepath.Join(files, name)); err != nil {
			t.Errorf("not in the trash as %s: %v", name, err)
		}
	}
	if runtime.GOOS != "darwin" {
		info, err := os.ReadFile(filepath.Join(home, "data", "Trash", "info", "notes 2.md.trashinfo"))
		if err != nil || !strings.Contains(string(info), "Path="+filepath.Join(dir, "notes.md")) {
			t.Errorf("the trash info: %q, %v", info, err)
		}
	}
}
