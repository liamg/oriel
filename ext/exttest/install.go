package exttest

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/ext/ossandbox"
)

// Install lays out the plugin in src as an install does, in a directory of the test's: src's files, and each Go
// command in cmds (a package directory, such as ./cmd/oriel-linear, relative to the test's working directory) built
// into its bin/ under the directory's name. It skips the test where the OS sandbox a plugin's processes run in
// isn't there.
func Install(t testing.TB, src string, cmds ...string) string {
	t.Helper()
	if len(cmds) > 0 {
		if err := ossandbox.Available(); err != nil {
			t.Skip(err)
		}
	}
	dir := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		to := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cmds {
		build := exec.Command("go", "build", "-buildvcs=false", "-o", filepath.Join(dir, "bin", filepath.Base(c)), c)
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("building %s: %v\n%s", c, err, out)
		}
	}
	return dir
}
