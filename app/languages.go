package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/toml"
)

// languagesPlugin is builtin:languages (ED-11): Oriel's core grammars, registered as a third-party pack would be, and
// the local packs in the config directory's languages/, which may shadow them. Grammars run sandboxed in wazero with
// no host imports, so a pack on disk needs no consent. languages.install adds a grammar pack CI built (PL-3).
type languagesPlugin struct {
	disposer
	dir      string          // ~/.config/oriel/languages
	cacheDir string          // where compiled grammars are kept
	sums     syntax.PackSums // the packs it installs; the binary's list when nil

	h     plugin.Host
	packs map[string]registry.Disposable // each local pack's registration, by its directory's name
}

func (x *languagesPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "languages"} }

func (x *languagesPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h, x.packs = h, map[string]registry.Disposable{}
	x.add(onDispose(func() { x.packs = nil }))
	x.add(h.Command(registry.Command{ID: "languages.install", Title: "Install the grammar pack in the .tar.gz at {path}, if it's one this build of Oriel lists",
		Run: func(_ registry.Ctx, a registry.Args) error { return x.install(a.String("path")) }}))
	if x.cacheDir != "" {
		syntax.SetCacheDir(x.cacheDir)
	}
	for _, l := range syntax.Builtin().Languages() {
		x.add(h.Language(registry.Language{Name: l.Name, Extensions: l.Extensions, Filenames: l.Filenames, Shebangs: l.Shebangs,
			Grammar: l.Grammar, Queries: l.Queries, Source: "builtin:languages"}))
	}
	entries, _ := os.ReadDir(x.dir)
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			x.load(e.Name())
		}
	}
	return nil
}

// load registers the pack in dir/name, in place of what it registered before.
func (x *languagesPlugin) load(name string) error {
	l, err := readPack(filepath.Join(x.dir, name))
	if err != nil {
		x.h.Log().Warn("language pack", "dir", name, "err", err)
		return err
	}
	if d := x.packs[name]; d != nil {
		d.Dispose()
	}
	d := x.h.Language(l)
	x.packs[name] = d
	x.add(d)
	return nil
}

// install puts the grammar pack at path into the languages directory and registers it. Only a pack matching the
// checksums the binary ships installs (deps.md). A grammar has no host imports, so there's no consent to ask.
func (x *languagesPlugin) install(path string) error {
	if path == "" {
		return fmt.Errorf("languages.install needs the pack's {path}")
	}
	sums := x.sums
	if sums == nil {
		sums = syntax.Packs()
	}
	f, err := os.Open(plugin.Expand(path))
	if err != nil {
		return err
	}
	defer f.Close()
	name, err := sums.Install(f, x.dir)
	if err != nil {
		return err
	}
	return x.load(name)
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
