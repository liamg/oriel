package runner

import (
	"debug/elf"
	"path/filepath"
	"testing"
)

func TestBuildLinuxOriel(t *testing.T) {
	if testing.Short() {
		t.Skip("builds oriel")
	}
	if _, err := findGo(); err != nil {
		t.Skip(err)
	}
	dst := filepath.Join(t.TempDir(), "bin", "oriel-linux")
	if err := buildLinuxOriel(dst); err != nil {
		t.Fatal(err)
	}
	f, err := elf.Open(dst)
	if err != nil {
		t.Fatalf("not a Linux binary: %v", err)
	}
	_ = f.Close()
}
