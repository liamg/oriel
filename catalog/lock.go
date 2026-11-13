package catalog

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/liamg/oriel/toml"
)

// LockFile is the lockfile's name, next to the settings.toml whose [plugins] it locks.
const LockFile = "oriel-lock.toml"

// Locked is what oriel-lock.toml records of one plugin: where it came from, the exact version and
// commit, its badge, the capabilities you accepted, as plugin.Lock writes them, and its archives' sha256s.
type Locked struct {
	Source       string
	Version      string
	Commit       string
	Badge        string
	Capabilities []string
	Archives     map[string]string // "darwin/arm64" → "sha256:…"
}

// Lock is an oriel-lock.toml, kept as its document so a write changes only the plugins that changed.
type Lock struct {
	Path string
	doc  *toml.Document
	read []byte // the file as loaded or last saved; nil when there was none
}

// LoadLock reads the lockfile at path; one that isn't there is empty.
func LoadLock(path string) (*Lock, error) {
	src, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	doc, err := toml.Parse(src)
	if err != nil {
		return nil, err
	}
	return &Lock{Path: path, doc: doc, read: src}, nil
}

// IDs are the plugins it locks, sorted.
func (l *Lock) IDs() []string { return slices.Sorted(maps.Keys(l.doc.Value())) }

// Get is what it records of id.
func (l *Lock) Get(id string) (Locked, bool) {
	t, ok := l.doc.Value()[id].(map[string]any)
	if !ok {
		return Locked{}, false
	}
	e := Locked{Source: str(t["source"]), Version: str(t["version"]), Commit: str(t["commit"]), Badge: str(t["badge"]),
		Capabilities: strs(t["capabilities"])}
	if a, ok := t["archives"].(map[string]any); ok {
		e.Archives = map[string]string{}
		for k, v := range a {
			e.Archives[k] = str(v)
		}
	}
	return e, true
}

// Set records e for id, in place of what was there.
func (l *Lock) Set(id string, e Locked) error {
	if old, ok := l.Get(id); ok && equalLocked(old, e) {
		return nil
	}
	l.doc.Delete(id)
	set := func(k string, v any) error { return l.doc.Set([]string{id, k}, v) }
	caps := make([]any, len(e.Capabilities))
	for i, c := range e.Capabilities {
		caps[i] = c
	}
	for _, kv := range []struct {
		k string
		v any
	}{{"source", e.Source}, {"version", e.Version}, {"commit", e.Commit}, {"badge", e.Badge}, {"capabilities", caps}} {
		if s, ok := kv.v.(string); ok && s == "" {
			continue
		}
		if err := set(kv.k, kv.v); err != nil {
			return err
		}
	}
	if len(e.Archives) > 0 {
		a := map[string]any{}
		for k, v := range e.Archives {
			a[k] = v
		}
		if err := set("archives", a); err != nil {
			return err
		}
	}
	return nil
}

// Delete drops id's entry.
func (l *Lock) Delete(id string) error {
	if _, ok := l.doc.Value()[id]; !ok {
		return nil
	}
	return l.doc.Delete(id)
}

// Save writes the lockfile; an empty one is removed. A lock unchanged since it was read leaves the file alone, so
// it never clobbers a write made since.
func (l *Lock) Save() error {
	if len(l.doc.Value()) == 0 {
		if l.read == nil {
			return nil
		}
		if err := os.Remove(l.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		l.read = nil
		return nil
	}
	b := l.doc.Bytes()
	if l.read != nil && slices.Equal(b, l.read) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(l.Path, b, 0o644); err != nil {
		return err
	}
	l.read = b
	return nil
}

func equalLocked(a, b Locked) bool {
	if a.Source != b.Source || a.Version != b.Version || a.Commit != b.Commit || a.Badge != b.Badge ||
		!slices.Equal(a.Capabilities, b.Capabilities) || len(a.Archives) != len(b.Archives) {
		return false
	}
	for k, v := range a.Archives {
		if b.Archives[k] != v {
			return false
		}
	}
	return true
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
