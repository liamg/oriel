// Package layout checks the repo's layout: one module, and the layering rules in Rules.
package layout

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Module is Oriel's module path.
const Module = "github.com/liamg/oriel"

// Root walks up from dir to the directory holding Oriel's go.mod.
func Root(dir string) (string, error) {
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && bytes.HasPrefix(b, []byte("module "+Module+"\n")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no Oriel go.mod above " + dir)
		}
		dir = parent
	}
}

// ModuleDirs returns every directory under root holding a go.mod, skipping vendor, testdata and dot directories.
func ModuleDirs(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != root && (d.Name() == "vendor" || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
			return filepath.SkipDir
		}
		if d.Name() == "go.mod" {
			rel, _ := filepath.Rel(root, filepath.Dir(path))
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out, err
}
