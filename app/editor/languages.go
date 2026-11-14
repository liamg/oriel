package editor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/syntax"
)

// Languages is builtin:languages: Oriel's core grammars, registered as a third-party pack would be, and
// the local packs in the config directory's languages/, which may shadow them. Grammars run sandboxed in wazero with
// no host imports, so a pack on disk needs no consent. languages.install adds a grammar pack CI built.
type Languages struct {
	kit.Disposer
	Dir  string          // ~/.config/oriel/languages
	Sums syntax.PackSums // the packs it installs; the binary's list when nil

	h     plugin.Host
	packs map[string]registry.Disposable // each local pack's registration, by its directory's name
}

func (x *Languages) Manifest() plugin.Manifest { return plugin.Manifest{ID: "languages"} }

func (x *Languages) Activate(_ context.Context, h plugin.Host) error {
	x.h, x.packs = h, map[string]registry.Disposable{}
	x.Add(registry.DisposeFunc(func() { x.packs = nil }))
	x.Add(h.Command(registry.Command{ID: "languages.install", Title: "Install the grammar pack in the .tar.gz at {path}, if it's one this build of Oriel lists",
		Run: func(_ registry.Ctx, a registry.Args) error { return x.install(a.String("path")) }}))
	for _, l := range syntax.Builtin().Languages() {
		x.Add(h.Language(registry.Language{Name: l.Name, Extensions: l.Extensions, Filenames: l.Filenames, Shebangs: l.Shebangs,
			Grammar: l.Grammar, Queries: l.Queries, Source: "builtin:languages"}))
	}
	entries, _ := os.ReadDir(x.Dir)
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			x.load(e.Name())
		}
	}
	return nil
}

// load registers the pack in dir/name, in place of what it registered before.
func (x *Languages) load(name string) error {
	l, err := plugin.ReadLanguagePack(filepath.Join(x.Dir, name))
	if err != nil {
		x.h.Log().Warn("language pack", "dir", name, "err", err)
		return err
	}
	if d := x.packs[name]; d != nil {
		d.Dispose()
	}
	d := x.h.Language(l)
	x.packs[name] = d
	x.Add(d)
	return nil
}

// install puts the grammar pack at path into the languages directory and registers it. Only a pack matching the
// checksums the binary ships installs. A grammar has no host imports, so there's no consent to ask.
func (x *Languages) install(path string) error {
	if path == "" {
		return fmt.Errorf("languages.install needs the pack's {path}")
	}
	sums := x.Sums
	if sums == nil {
		sums = syntax.Packs()
	}
	f, err := os.Open(plugin.Expand(path))
	if err != nil {
		return err
	}
	defer f.Close()
	name, err := sums.Install(f, x.Dir)
	if err != nil {
		return err
	}
	return x.load(name)
}
