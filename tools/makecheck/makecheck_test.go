// Package makecheck holds the Makefile to the tree: a source file a rule names must exist, so moving one breaks a
// test here, not a release build on a machine that runs that rule.
package makecheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sourceFile is a path a Makefile rule may name that should exist in the repo: a Go, shell or Swift source.
var sourceFile = regexp.MustCompile(`^[A-Za-z0-9_./-]+\.(go|sh|swift|m|c|h)$`)

func TestMakefileSourcesExist(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for i, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "\t") {
			continue
		}
		_, deps, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(deps, "=") {
			continue
		}
		for _, f := range strings.Fields(deps) {
			if !sourceFile.MatchString(f) || strings.Contains(f, "$") {
				continue
			}
			n++
			if _, err := os.Stat(filepath.Join(root, f)); err != nil {
				t.Errorf("Makefile:%d names %s, which doesn't exist", i+1, f)
			}
		}
	}
	if n == 0 {
		t.Fatal("found no source files named by the Makefile's rules: the parse missed them")
	}
}
