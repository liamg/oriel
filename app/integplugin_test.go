package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/ext/ossandbox"
)

// integPlugin is integ/<name>'s plugin as an install lays it out: files from its plugin/ directory, and its process
// (cmd/oriel-<name>) built into bin/. It skips where the OS sandbox the process runs in isn't there.
func integPlugin(t *testing.T, name string, files ...string) (string, ext.Manifest) {
	t.Helper()
	if err := ossandbox.Available(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join("..", "integ", name, "plugin", f))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, f), b, 0o644)
	}
	bin := "oriel-" + name
	build := exec.Command("go", "build", "-buildvcs=false", "-o", filepath.Join(dir, "bin", bin), "./integ/"+name+"/cmd/"+bin)
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the plugin's process: %v\n%s", err, out)
	}
	m, err := ext.LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, m
}
