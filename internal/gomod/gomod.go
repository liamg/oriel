// Package gomod reads Go modules' requirements (go.mod, vendor/modules.txt) and checks them against an allowlist
// of approved major versions: what tools/depcheck holds Oriel to, and what a plugin's PR check shows its author.
package gomod

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// Workspace is the module path prefix of our own modules, which need no approval.
const Workspace = "github.com/liamg/oriel"

// Module is one required module.
type Module struct {
	Path    string
	Version string
	From    string // the go.mod or modules.txt that requires it
}

// Major is the module's major version, e.g. "v0", "v1", "v2".
func (m Module) Major() string {
	v := strings.TrimPrefix(m.Version, "v")
	if i := strings.IndexAny(v, ".-+"); i >= 0 {
		v = v[:i]
	}
	return "v" + v
}

// Allowlist maps a module path to its approved major versions.
type Allowlist map[string]map[string]bool

// ParseAllowlist reads lines of "path vN"; '#' starts a comment.
func ParseAllowlist(r io.Reader) (Allowlist, error) {
	a := Allowlist{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line, _, _ := strings.Cut(sc.Text(), "#")
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 || !majorRE.MatchString(f[1]) {
			return nil, fmt.Errorf("allowed.txt:%d: want \"module/path vN\", got %q", n, sc.Text())
		}
		if a[f[0]] == nil {
			a[f[0]] = map[string]bool{}
		}
		a[f[0]][f[1]] = true
	}
	return a, sc.Err()
}

var majorRE = regexp.MustCompile(`^v\d+$`)

// Requires reads the require directives of a go.mod.
func Requires(gomod string) ([]Module, error) {
	b, err := os.ReadFile(gomod)
	if err != nil {
		return nil, err
	}
	var out []Module
	inBlock := false
	for _, line := range strings.Split(string(b), "\n") {
		line, _, _ = strings.Cut(line, "//")
		f := strings.Fields(line)
		switch {
		case len(f) == 0:
		case inBlock && f[0] == ")":
			inBlock = false
		case inBlock && len(f) == 2:
			out = append(out, Module{f[0], f[1], gomod})
		case f[0] == "require" && len(f) == 2 && f[1] == "(":
			inBlock = true
		case f[0] == "require" && len(f) == 3:
			out = append(out, Module{f[1], f[2], gomod})
		}
	}
	return out, nil
}

// Vendored reads the modules listed in a vendor/modules.txt.
func Vendored(modulesTxt string) ([]Module, error) {
	b, err := os.ReadFile(modulesTxt)
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var out []Module
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == "#" && !strings.HasPrefix(f[1], "#") && strings.HasPrefix(f[2], "v") {
			out = append(out, Module{f[1], f[2], modulesTxt})
		}
	}
	return out, nil
}

// Outside are the modules in mods that aren't the workspace's own and aren't allowed, each path and major once, in
// the order met.
func Outside(allow Allowlist, mods []Module) []Module {
	var out []Module
	seen := map[string]bool{}
	for _, m := range mods {
		if m.Path == Workspace || strings.HasPrefix(m.Path, Workspace+"/") {
			continue
		}
		key := m.Path + " " + m.Major()
		if seen[key] {
			continue
		}
		seen[key] = true
		if !allow[m.Path][m.Major()] {
			out = append(out, m)
		}
	}
	return out
}
