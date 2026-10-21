package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// A sandbox dir goes when its workspace is gone and no session claims it; one whose workspace exists, or that a
// session still has, stays.
func TestSweepRemovesOrphanDirs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspaces := t.TempDir()
	gone, kept, claimed := filepath.Join(workspaces, "gone"), filepath.Join(workspaces, "kept"), filepath.Join(workspaces, "claimed")
	for _, root := range []string{gone, kept, claimed} {
		if _, err := Create(root, ModeSandbox); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(kept, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(Base(), "bin", "oriel"), 10)
	removed := Sweep(func(root string) bool { return root == claimed }, Config{GoCacheMax: "none"})
	if !slices.Equal(removed, []string{Dir(gone)}) {
		t.Errorf("removed %v, want only %s", removed, Dir(gone))
	}
	if exists(Dir(gone)) || !exists(Dir(kept)) || !exists(Dir(claimed)) || !exists(filepath.Join(Base(), "bin")) {
		t.Errorf("gone %v, kept %v, claimed %v, bin %v", exists(Dir(gone)), exists(Dir(kept)), exists(Dir(claimed)), exists(filepath.Join(Base(), "bin")))
	}
}

// The Go build cache is trimmed oldest-used first, and only until it's under the cap.
func TestTrimCacheRemovesLeastRecentlyUsed(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	var paths []string
	for i, name := range []string{"00/old-a", "01/older-d", "02/new-a", "03/newest-d"} {
		p := filepath.Join(dir, name)
		writeFile(t, p, 100)
		at := now.Add(-time.Duration([]int{3, 4, 2, 1}[i]) * time.Hour)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	writeFile(t, filepath.Join(dir, "README"), 1000)
	n, freed := TrimCache(dir, 250)
	if n != 2 || freed != 200 {
		t.Errorf("removed %d files, %d bytes; want 2, 200", n, freed)
	}
	for i, want := range []bool{false, false, true, true} {
		if exists(paths[i]) != want {
			t.Errorf("%s exists = %v, want %v", paths[i], !want, want)
		}
	}
	if !exists(filepath.Join(dir, "README")) {
		t.Error("the trim removed a file that isn't a cache entry")
	}
	if n, _ := TrimCache(dir, 250); n != 0 {
		t.Errorf("a cache under its cap lost %d files", n)
	}
}

// Clean empties the temp dir and the home's caches, and keeps the rest of the sandbox.
func TestCleanKeepsWhatReopenNeeds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "ws")
	if _, err := Create(root, ModeSandbox); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(TmpDir(root), "scratch", "notes.md"), 10)
	writeFile(t, filepath.Join(HomeDir(root), ".cache", "pip", "x"), 10)
	writeFile(t, filepath.Join(HomeDir(root), ".npm", "x"), 10)
	writeFile(t, filepath.Join(HomeDir(root), ".gitconfig"), 10)
	if err := Clean(root); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(TmpDir(root)); err != nil || len(entries) != 0 {
		t.Errorf("tmp = %v, %v; want empty", entries, err)
	}
	if exists(filepath.Join(HomeDir(root), ".cache")) || exists(filepath.Join(HomeDir(root), ".npm")) {
		t.Error("the home's caches stayed")
	}
	if !exists(filepath.Join(HomeDir(root), ".gitconfig")) || !Managed(root) {
		t.Error("Clean removed what a reopen needs")
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"20g": 20 << 30, "512M": 512 << 20, "1024": 1024, "8k": 8 << 10, "3b": 3} {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "g", "big", "-1g"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) took it", bad)
		}
	}
}
