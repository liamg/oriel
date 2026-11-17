package app_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A built-in plugin's id is kebab-case, lower-case words joined by "-" (docs/architecture.md, "Plugin ids"), as a
// third-party manifest's <owner>/<name> parts are: never dotted, which reads as a command, nor run together.
func TestBuiltinPluginIDsAreKebabCase(t *testing.T) {
	declared := regexp.MustCompile(`plugin\.Manifest\{ID: "([^"]*)"`)
	kebab := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	n := 0
	for _, root := range []string{".", "../integ"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) && path != root {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(path, "apptest") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range declared.FindAllStringSubmatch(string(src), -1) {
				n++
				if !kebab.MatchString(m[1]) {
					t.Errorf("%s: plugin id %q isn't kebab-case", path, m[1])
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n < 40 {
		t.Fatalf("found %d plugin ids: the scan missed them", n)
	}
}
