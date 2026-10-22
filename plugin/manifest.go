package plugin

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/liamg/oriel/toml"
)

// ManifestFile is a plugin's manifest, at its root.
const ManifestFile = "oriel.toml"

// Manifest is what a plugin says about itself. Built-ins fill in only ID.
type Manifest struct {
	ID          string // <owner>/<name>
	Version     string
	Oriel       string // the Oriel versions it needs: ">=0.9"
	Description string
	License     string
	Icon        string
	Runs        string   // app, session or both; app when ""
	Lazy        Lazy     // when to load it; at start when empty
	Binaries    []string // the os/arch pairs it ships a process binary for
	Permissions []Grant  // what it declares, sorted by capability
	Contributes Contributes
}

// CheckID says why id isn't <owner>/<name>: two plain path elements, neither "." nor "..".
func CheckID(id string) error {
	owner, name, ok := strings.Cut(id, "/")
	if !ok || !fs.ValidPath(id) || strings.Contains(name, "/") || strings.Contains(id, `\`) ||
		owner == "." || owner == ".." || name == "." || name == ".." {
		return fmt.Errorf("id %q isn't <owner>/<name>", id)
	}
	return nil
}

// LoadManifest reads dir's oriel.toml.
func LoadManifest(dir string) (Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return Manifest{}, err
	}
	return ParseManifest(b)
}

// ParseManifest reads an oriel.toml. A capability Oriel doesn't know is an error, so nothing is granted by a typo.
func ParseManifest(src []byte) (Manifest, error) {
	doc, err := toml.Parse(src)
	if err != nil {
		return Manifest{}, err
	}
	v := doc.Value()
	m := Manifest{ID: tstr(v["id"]), Version: tstr(v["version"]), Oriel: tstr(v["oriel"]), Description: tstr(v["description"]),
		License: tstr(v["license"]), Icon: tstr(v["icon"]), Runs: tstr(v["runs"]), Binaries: tstrs(v["binaries"])}
	if err := CheckID(m.ID); err != nil {
		return m, fmt.Errorf("oriel.toml: %w", err)
	}
	if m.Version != "" {
		if _, err := ParseVersion(m.Version); err != nil {
			return m, fmt.Errorf("oriel.toml: version: %w", err)
		}
	}
	switch m.Runs {
	case "", "app", "session", "both":
	default:
		return m, fmt.Errorf("oriel.toml: runs is app, session or both, not %q", m.Runs)
	}
	for _, b := range m.Binaries {
		if !slices.Contains(Platforms, b) {
			return m, fmt.Errorf("oriel.toml: binaries: %q isn't one of %s", b, strings.Join(Platforms, ", "))
		}
	}
	if c, ok := v["contributes"].(map[string]any); ok {
		if m.Contributes, err = contributes(c); err != nil {
			return m, err
		}
	}
	if l, ok := v["lazy"].(map[string]any); ok {
		m.Lazy = ParseLazy(l)
	}
	if p, ok := v["permissions"].(map[string]any); ok {
		if m.Permissions, err = permissions("", p); err != nil {
			return m, err
		}
		sort.Slice(m.Permissions, func(i, j int) bool { return m.Permissions[i].Cap < m.Permissions[j].Cap })
	}
	return m, nil
}

// permissions reads [permissions]. A dotted key (fs.read = …) arrives as a table under its first part.
func permissions(prefix string, t map[string]any) ([]Grant, error) {
	var out []Grant
	for k, v := range t {
		name := prefix + k
		c := Capability(name)
		info, known := c.Info()
		if !known {
			sub, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("oriel.toml: unknown capability %q", name)
			}
			gs, err := permissions(name+".", sub)
			if err != nil {
				return nil, err
			}
			out = append(out, gs...)
			continue
		}
		g := Grant{Cap: c}
		switch v := v.(type) {
		case bool:
			if !v {
				continue
			}
		case string, []any:
			g.Scope = tstrs(v)
		case map[string]any:
			g.Why = tstr(v["why"])
			for sk, sv := range v {
				switch {
				case sk == "why":
				case c == CapSecrets && tstr(sv) != "":
					g.Scope = append(g.Scope, sk+"@"+tstr(sv)) // NAME = "host": bound to the host
				case slices.Contains([]string{"hosts", "paths", "scope", "commands", "tools", "names"}, sk):
					g.Scope = append(g.Scope, tstrs(sv)...)
				default:
					return nil, fmt.Errorf("oriel.toml: %s doesn't take %q", name, sk)
				}
			}
			sort.Strings(g.Scope)
		default:
			return nil, fmt.Errorf("oriel.toml: %s is a list, a table or true", name)
		}
		if info.Scope != "" && len(g.Scope) == 0 {
			return nil, fmt.Errorf("oriel.toml: %s needs its %s", name, info.Scope)
		}
		out = append(out, g)
	}
	return out, nil
}

func tstr(v any) string {
	s, _ := v.(string)
	return s
}

func tstrs(v any) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
