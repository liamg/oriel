// Package layout checks the repo's module layout against _design/architecture.md.
package layout

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Root walks up from dir to the directory holding go.work.
func Root(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.work above " + dir)
		}
		dir = parent
	}
}

var codeSpan = regexp.MustCompile("`([^`]+)`")

// DesignModules returns the paths named in the first column of the module table in architecture.md.
func DesignModules(root string) ([]string, error) {
	f, err := os.Open(filepath.Join(root, "_design", "architecture.md"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	inTable := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "| Module |"):
			inTable = true
			continue
		case !strings.HasPrefix(line, "|"):
			inTable = false
			continue
		case !inTable || strings.HasPrefix(line, "|---"):
			continue
		}
		cells := strings.Split(line, "|")
		for _, m := range codeSpan.FindAllStringSubmatch(cells[1], -1) {
			out = append(out, strings.TrimSuffix(m[1], "/*"))
		}
	}
	return out, sc.Err()
}

// WorkModules returns the directories listed in go.work's use block.
func WorkModules(root string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		return nil, err
	}
	var out []string
	inUse := false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "use (":
			inUse = true
		case line == ")":
			inUse = false
		case inUse && line != "":
			out = append(out, filepath.Clean(line))
		}
	}
	return out, nil
}

// ModuleDirs returns every directory under root holding a go.mod, skipping vendor and dot directories.
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
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}
