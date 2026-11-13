// Package settingsfile is what the app and a worker share about settings.toml files: the trust a project's file
// needs before it applies, and how a file's keys spell option names.
package settingsfile

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/internal/atomicfile"
	"github.com/liamg/oriel/toml"
)

// Trust is the trusted.toml at Path: each trusted project file's SHA-256, by its path, so a change needs trusting
// again.
type Trust struct{ Path string }

// Trusted reports whether file, as src, is trusted.
func (t Trust) Trusted(file string, src []byte) bool {
	b, err := os.ReadFile(t.Path)
	if err != nil {
		return false
	}
	doc, err := toml.Parse(b)
	if err != nil {
		return false
	}
	v, _ := doc.Get("projects", file)
	return v == hash(src)
}

// Add trusts file as src is now.
func (t Trust) Add(file string, src []byte) error {
	b, err := os.ReadFile(t.Path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	doc, err := toml.Parse(b)
	if err != nil {
		return err
	}
	if err := doc.Set([]string{"projects", file}, hash(src)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(t.Path), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(t.Path, doc.Bytes(), 0o644)
}

func hash(src []byte) string {
	h := sha256.Sum256(src)
	return hex.EncodeToString(h[:])
}

// Key is a key of a settings file: the option name it spells (dotted, from its tables), where it is, and its value.
type Key struct {
	Name  string
	Path  []string
	Value any
}

// Keys are m's keys as option names, sorted. A table is read as the keys under it, unless leaf says its name is a
// value of its own (a map option's).
func Keys(m map[string]any, leaf func(name string) bool) []Key {
	return keys(m, nil, leaf)
}

func keys(m map[string]any, prefix []string, leaf func(string) bool) []Key {
	var out []Key
	for _, k := range slices.Sorted(maps.Keys(m)) {
		p := append(slices.Clone(prefix), k)
		name := strings.Join(p, ".")
		if sub, ok := m[k].(map[string]any); ok && (leaf == nil || !leaf(name)) {
			out = append(out, keys(sub, p, leaf)...)
			continue
		}
		out = append(out, Key{Name: name, Path: p, Value: m[k]})
	}
	return out
}

// Strings is a list of strings from a file or Lua ([]any), or one already.
func Strings(v any) ([]string, bool) {
	switch v := v.(type) {
	case []string:
		return v, true
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			s, ok := x.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

// StringMap is a table of strings from a file or Lua (map[string]any), or one already.
func StringMap(v any) (map[string]string, bool) {
	switch v := v.(type) {
	case map[string]string:
		return v, true
	case map[string]any:
		out := make(map[string]string, len(v))
		for k, x := range v {
			s, ok := x.(string)
			if !ok {
				return nil, false
			}
			out[k] = s
		}
		return out, true
	}
	return nil, false
}
