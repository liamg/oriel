package catalog

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/plugin"
)

// Entry is one line of [plugins] (catalog.md): a plugin by id, from the registries in a semver range, or from a git
// repository or a local directory.
type Entry struct {
	ID      string
	Range   string // a semver range, resolved through the registries: "^0.4", "*"
	Git     string // a repository: "github.com/someone/thing", or any URL git clones
	Rev     string // the git source's tag, branch or commit; its default branch if ""
	Path    string // a local directory, for development
	Enabled bool   // false: installed, but not loaded
	Lazy    *plugin.Lazy
	// File is the settings.toml it's from, and Project the project's root for a project's file ("" for yours).
	File, Project string
}

// Source names where an entry comes from, as the lockfile records it: "registry", "git:github.com/x/y",
// "path:~/src/wip".
func (e Entry) Source() string {
	switch {
	case e.Git != "":
		return "git:" + e.Git
	case e.Path != "":
		return "path:" + e.Path
	}
	return "registry"
}

// Registry says the entry is resolved through the plugin registries.
func (e Entry) Registry() bool { return e.Git == "" && e.Path == "" }

// ParseEntries reads a [plugins] table: a string is a range, a table has version, git (with rev) or path, and
// enabled and lazy. Each entry that can't be read is an error of its own; the rest are still returned.
func ParseEntries(t map[string]any) ([]Entry, []error) {
	var out []Entry
	var errs []error
	for _, id := range sortedKeys(t) {
		e := Entry{ID: id, Enabled: true}
		if owner, name, ok := strings.Cut(id, "/"); !ok || owner == "" || name == "" || strings.Contains(name, "/") {
			errs = append(errs, fmt.Errorf("[plugins] %q isn't <owner>/<name>", id))
			continue
		}
		switch v := t[id].(type) {
		case string:
			e.Range = v
		case map[string]any:
			for k, x := range v {
				var ok bool
				switch k {
				case "version":
					e.Range, ok = x.(string)
				case "git":
					e.Git, ok = x.(string)
				case "rev":
					e.Rev, ok = x.(string)
				case "path":
					e.Path, ok = x.(string)
				case "enabled":
					e.Enabled, ok = x.(bool)
				case "lazy":
					var l map[string]any
					if l, ok = x.(map[string]any); ok {
						lz := plugin.Lazy{Commands: strs(l["commands"]), Events: strs(l["events"]), Keys: strs(l["keys"]),
							Filetypes: strs(l["filetypes"]), Omnibox: strs(l["omnibox"])}
						e.Lazy = &lz
					}
				default:
					errs = append(errs, fmt.Errorf("[plugins] %q: no such key %q", id, k))
					ok = true
				}
				if !ok {
					errs = append(errs, fmt.Errorf("[plugins] %q: %s has the wrong type", id, k))
				}
			}
		default:
			errs = append(errs, fmt.Errorf("[plugins] %q is a version range or a table", id))
			continue
		}
		n := 0
		for _, s := range []string{e.Git, e.Path} {
			if s != "" {
				n++
			}
		}
		switch {
		case n > 1:
			errs = append(errs, fmt.Errorf("[plugins] %q: git and path are two sources; give one", id))
			continue
		case n == 0 && e.Range == "":
			e.Range = "*"
		case n == 1 && e.Range != "":
			errs = append(errs, fmt.Errorf("[plugins] %q: a version is for the registries, not a git or path source", id))
			continue
		}
		if e.Range != "" {
			if _, err := plugin.ParseRange(e.Range); err != nil {
				errs = append(errs, fmt.Errorf("[plugins] %q: %w", id, err))
				continue
			}
		}
		out = append(out, e)
	}
	return out, errs
}

// GitURL is what git clones for a git source: a host path is fetched over https.
func (e Entry) GitURL() string {
	g := e.Git
	if strings.Contains(g, "://") || strings.HasPrefix(g, "git@") || filepath.IsAbs(g) || strings.HasPrefix(g, "~") {
		return plugin.Expand(g)
	}
	return "https://" + g
}

func strs(v any) []string {
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

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}
