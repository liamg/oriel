package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/toml"
)

// languagesExt is builtin:languages (ED-11): Oriel's core grammars, registered as a third-party pack would be, and
// the local packs in the config directory's languages/, which may shadow them. Grammars run sandboxed in wazero with
// no host imports, so a pack on disk needs no consent.
type languagesExt struct {
	disposer
	dir      string // ~/.config/oriel/languages
	cacheDir string // where compiled grammars are kept
}

func (x *languagesExt) Manifest() ext.Manifest { return ext.Manifest{ID: "languages"} }

func (x *languagesExt) Activate(_ context.Context, h ext.Host) error {
	if x.cacheDir != "" {
		syntax.SetCacheDir(x.cacheDir)
	}
	for _, l := range syntax.Builtin().Languages() {
		x.add(h.Language(registry.Language{Name: l.Name, Extensions: l.Extensions, Filenames: l.Filenames, Shebangs: l.Shebangs,
			Grammar: l.Grammar, Queries: l.Queries, Source: "builtin:languages"}))
	}
	entries, _ := os.ReadDir(x.dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		l, err := readPack(filepath.Join(x.dir, e.Name()))
		if err != nil {
			h.Log().Warn("language pack", "dir", e.Name(), "err", err)
			continue
		}
		x.add(h.Language(l))
	}
	return nil
}

// readPack reads a grammar pack: language.toml (name, extensions, filenames, shebangs), grammar.wasm and queries/.
func readPack(dir string) (registry.Language, error) {
	l := registry.Language{Name: filepath.Base(dir), Source: dir, Queries: map[string]string{}}
	if src, err := os.ReadFile(filepath.Join(dir, "language.toml")); err == nil {
		doc, err := toml.Parse(src)
		if err != nil {
			return l, err
		}
		v := doc.Value()
		if n, ok := v["name"].(string); ok {
			l.Name = n
		}
		strs := func(k string) []string {
			var out []string
			if xs, ok := v[k].([]any); ok {
				for _, x := range xs {
					if s, ok := x.(string); ok {
						out = append(out, s)
					}
				}
			}
			return out
		}
		l.Extensions, l.Filenames, l.Shebangs = strs("extensions"), strs("filenames"), strs("shebangs")
	}
	wasm := filepath.Join(dir, "grammar.wasm")
	if _, err := os.Stat(wasm); err == nil {
		l.Grammar = func() ([]byte, error) { return os.ReadFile(wasm) }
	}
	qs, _ := filepath.Glob(filepath.Join(dir, "queries", "*.scm"))
	for _, q := range qs {
		b, err := os.ReadFile(q)
		if err != nil {
			return l, err
		}
		l.Queries[strings.TrimSuffix(filepath.Base(q), ".scm")] = string(b)
	}
	return l, nil
}

// langSets are the registry's languages as a syntax.Set, made again when they change.
var langSets struct {
	sync.Mutex
	by map[registry.Registry]*langSet
}

type langSet struct {
	gen int
	set *syntax.Set
}

// languages is the set of languages r has now.
func languages(r registry.Registry) *syntax.Set {
	ls, gen := r.Languages()
	langSets.Lock()
	defer langSets.Unlock()
	if langSets.by == nil {
		langSets.by = map[registry.Registry]*langSet{}
	}
	if s, ok := langSets.by[r]; ok && s.gen == gen {
		return s.set
	}
	var out []*syntax.Language
	for _, l := range ls {
		out = append(out, &syntax.Language{Name: l.Name, Extensions: l.Extensions, Filenames: l.Filenames, Shebangs: l.Shebangs,
			Grammar: l.Grammar, Queries: l.Queries})
	}
	s := &langSet{gen: gen, set: syntax.NewSet(out...)}
	langSets.by[r] = s
	return s.set
}

// languageFor is the language of the file at path, by its name or the first line of src.
func languageFor(r registry.Registry, path string, src []byte) (*syntax.Language, bool) {
	first, _, _ := bytes.Cut(src[:min(len(src), 512)], []byte("\n"))
	return languages(r).ForFile(path, first)
}
