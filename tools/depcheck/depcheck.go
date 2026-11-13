// Package depcheck checks every module the workspace requires against the allowlist in allowed.txt.
package depcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/internal/gomod"
)

// AllowedFile is the allowlist, relative to the repo root.
const AllowedFile = "tools/depcheck/allowed.txt"

// Module is one required module (gomod.Module).
type Module = gomod.Module

// Allowlist maps a module path to its approved major versions (gomod.Allowlist).
type Allowlist = gomod.Allowlist

// Check returns a problem for every module in mods that isn't ours and isn't allowed.
func Check(allow Allowlist, mods []Module) []string {
	var problems []string
	for _, m := range gomod.Outside(allow, mods) {
		problems = append(problems, fmt.Sprintf("%s %s (required by %s) is not in tools/depcheck/allowed.txt; ask before adding it", m.Path, m.Version, m.From))
	}
	slices.Sort(problems)
	return problems
}

// Run checks every go.mod under root plus vendor/modules.txt.
func Run(root string) ([]string, error) {
	f, err := os.Open(filepath.Join(root, AllowedFile))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	allow, err := gomod.ParseAllowlist(f)
	if err != nil {
		return nil, err
	}
	var mods []Module
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != root && (d.Name() == "vendor" || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
			return filepath.SkipDir
		}
		if d.Name() != "go.mod" {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		ms, err := gomod.Requires(path)
		for i := range ms {
			ms[i].From = rel
		}
		mods = append(mods, ms...)
		return err
	})
	if err != nil {
		return nil, err
	}
	vendored, err := gomod.Vendored(filepath.Join(root, "vendor", "modules.txt"))
	if err != nil {
		return nil, err
	}
	for i := range vendored {
		vendored[i].From = "vendor/modules.txt"
	}
	return Check(allow, append(mods, vendored...)), nil
}
