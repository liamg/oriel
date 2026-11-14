package plugin

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing/fstest"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/toml"
)

// Platforms are the os/arch pairs a plugin can ship a process binary for.
var Platforms = []string{"darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64"}

// MaxSize is the most a plugin may be, unpacked.
const MaxSize = 20 << 20

// EntryFile is a plugin's entry point; a data-only plugin has none.
const EntryFile = "init.lua"

// AssetsDir holds a plugin's read-only files: oriel.asset reads under it.
const AssetsDir = "assets"

// Contributes is what a plugin registers by declaration alone, with no Lua: globs of its own
// files, and a directory of icons.
type Contributes struct {
	Themes, Fonts, Languages, Prompts, Help []string
	Icons                                   string
}

// Empty says it contributes nothing.
func (c Contributes) Empty() bool {
	return len(c.Themes)+len(c.Fonts)+len(c.Languages)+len(c.Prompts)+len(c.Help) == 0 && c.Icons == ""
}

// Patterns are its globs and the icons directory, each with its key.
func (c Contributes) Patterns() [][2]string {
	var out [][2]string
	for _, kv := range []struct {
		k  string
		vs []string
	}{{"themes", c.Themes}, {"fonts", c.Fonts}, {"icons", []string{c.Icons}}, {"languages", c.Languages},
		{"prompts", c.Prompts}, {"help", c.Help}} {
		for _, v := range kv.vs {
			if v != "" {
				out = append(out, [2]string{kv.k, v})
			}
		}
	}
	return out
}

func contributes(t map[string]any) (Contributes, error) {
	c := Contributes{Themes: Strings(t["themes"]), Fonts: Strings(t["fonts"]), Languages: Strings(t["languages"]),
		Prompts: Strings(t["prompts"]), Help: Strings(t["help"]), Icons: tstr(t["icons"])}
	for k := range t {
		switch k {
		case "themes", "fonts", "icons", "languages", "prompts", "help":
		default:
			return c, fmt.Errorf("oriel.toml: [contributes] has no %q", k)
		}
	}
	for _, p := range c.Patterns() {
		if !local(p[1]) {
			return c, fmt.Errorf("oriel.toml: [contributes] %s: %q isn't a path in the plugin", p[0], p[1])
		}
		if _, err := path.Match(p[1], ""); err != nil {
			return c, fmt.Errorf("oriel.toml: [contributes] %s: %q: %w", p[0], p[1], err)
		}
	}
	return c, nil
}

// local is a slash-separated path that stays in the directory it's relative to.
func local(p string) bool {
	return p != "" && fs.ValidPath(strings.TrimSuffix(p, "/")) && !strings.Contains(p, "\\")
}

// Files are the plugin's files a pattern matches, in dir, symlinks out of it left out.
func Files(dir, pattern string) ([]string, error) {
	ms, err := filepath.Glob(filepath.Join(dir, filepath.FromSlash(pattern)))
	if err != nil {
		return nil, err
	}
	p := Caller{Dir: dir}
	var out []string
	for _, m := range ms {
		if p.Own(m) {
			out = append(out, m)
		}
	}
	return out, nil
}

// pluginFiles are a plugin's files, in its directory or an fs.FS: those a pattern matches, and each one's bytes and
// base name.
type pluginFiles struct {
	match func(pattern string) ([]string, error)
	read  func(name string) ([]byte, error)
	base  func(name string) string
}

// dirFiles are the files in dir, symlinks out of it left out.
func dirFiles(dir string) pluginFiles {
	return pluginFiles{match: func(p string) ([]string, error) { return Files(dir, p) }, read: os.ReadFile, base: filepath.Base}
}

// fsFiles are the files in fsys.
func fsFiles(fsys fs.FS) pluginFiles {
	return pluginFiles{
		match: func(p string) ([]string, error) { return fs.Glob(fsys, strings.TrimSuffix(p, "/")) },
		read:  func(name string) ([]byte, error) { return fs.ReadFile(fsys, name) },
		base:  path.Base,
	}
}

// each runs do on every file the patterns under key match. A pattern matching nothing is an error, so a typo
// doesn't pass for an empty plugin.
func (f pluginFiles) each(key string, patterns []string, do func(name string) error) error {
	for _, p := range patterns {
		ms, err := f.match(p)
		if err == nil && len(ms) == 0 {
			err = fmt.Errorf("[contributes] %s: %q matches nothing", key, p)
		}
		if err != nil {
			return err
		}
		for _, m := range ms {
			if err := do(m); err != nil {
				return err
			}
		}
	}
	return nil
}

// texts registers the plugin's prompt packs and help pages, which need only their files' bytes.
func (f pluginFiles) texts(r registry.Registry, source string, c Contributes, ds *disposables) error {
	err := f.each("prompts", c.Prompts, func(name string) error {
		b, err := f.read(name)
		if err != nil {
			return err
		}
		base := f.base(name)
		*ds = append(*ds, r.Contribute(registry.PromptPack{Name: strings.TrimSuffix(base, path.Ext(base)), Prompt: string(b), Source: source}))
		return nil
	})
	if err != nil || len(c.Help) == 0 {
		return err
	}
	pages := fstest.MapFS{}
	err = f.each("help", c.Help, func(name string) error {
		b, err := f.read(name)
		if err == nil {
			pages[f.base(name)] = &fstest.MapFile{Data: b, Mode: 0o444}
		}
		return err
	})
	if err != nil {
		return err
	}
	*ds = append(*ds, r.HelpPages(registry.HelpSource{FS: pages, Source: source}))
	return nil
}

// Contribute registers what c declares from the plugin in dir, under source (its id): themes, grammar packs,
// prompt packs and help pages, and its fonts and icons as contributions of kinds "font" and "icon". A pattern
// matching nothing is an error, so a typo doesn't pass for an empty plugin.
func Contribute(r registry.Registry, dir, source string, c Contributes) (registry.Disposable, error) {
	var ds disposables
	f := dirFiles(dir)
	err := f.each("themes", c.Themes, func(name string) error {
		if _, err := (theme.File{Path: name}).Load(); err != nil {
			return err
		}
		ds = append(ds, r.Theme(theme.File{Path: name}))
		return nil
	})
	if err == nil {
		err = f.each("languages", c.Languages, func(name string) error {
			l, err := ReadLanguagePack(name)
			if err == nil {
				l.Source = source
				ds = append(ds, r.Language(l))
			}
			return err
		})
	}
	if err == nil {
		err = f.texts(r, source, c, &ds)
	}
	if err == nil {
		err = f.each("fonts", c.Fonts, func(name string) error {
			ds = append(ds, r.Contribute(registry.Font{Name: filepath.Base(name), Path: name, Source: source}))
			return nil
		})
	}
	if err == nil && c.Icons != "" {
		err = dirIcons(r, dir, source, c.Icons, &ds)
	}
	if err != nil {
		ds.Dispose()
		return nil, err
	}
	return ds, nil
}

// dirIcons registers each icon in the plugin's icons directory, by its path.
func dirIcons(r registry.Registry, dir, source, icons string, ds *disposables) error {
	at := filepath.Join(dir, filepath.FromSlash(strings.TrimSuffix(icons, "/")))
	if !(Caller{Dir: dir}).Own(at) {
		return fmt.Errorf("[contributes] icons: %q isn't a directory in the plugin", icons)
	}
	es, err := os.ReadDir(at)
	if err != nil {
		return err
	}
	for _, e := range es {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		*ds = append(*ds, r.Contribute(registry.Icon{Name: name, Path: filepath.Join(at, e.Name()), Source: source}))
	}
	return nil
}

// ContributeFS is Contribute for a plugin with no directory, its files in fsys (a bundled plugin's): its prompt
// packs, help pages and icons. Themes, grammar packs and fonts are read from a directory, so they need one.
func ContributeFS(r registry.Registry, fsys fs.FS, source string, c Contributes) (registry.Disposable, error) {
	if len(c.Themes)+len(c.Languages)+len(c.Fonts) > 0 {
		return nil, errors.New("[contributes] themes, languages and fonts need the plugin's directory")
	}
	var ds disposables
	err := fsFiles(fsys).texts(r, source, c, &ds)
	if err == nil && c.Icons != "" {
		err = fsIcons(r, fsys, source, c.Icons, &ds)
	}
	if err != nil {
		ds.Dispose()
		return nil, err
	}
	return ds, nil
}

// fsIcons registers each icon in fsys's icons directory, by its bytes.
func fsIcons(r registry.Registry, fsys fs.FS, source, icons string, ds *disposables) error {
	dir := strings.TrimSuffix(icons, "/")
	es, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return fmt.Errorf("[contributes] icons: %w", err)
	}
	for _, e := range es {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		b, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(e.Name(), path.Ext(e.Name()))
		*ds = append(*ds, r.Contribute(registry.Icon{Name: name, Data: b, Source: source}))
	}
	return nil
}

type disposables []registry.Disposable

func (ds disposables) Dispose() {
	for _, d := range ds {
		d.Dispose()
	}
}

// ReadLanguagePack reads a grammar pack: language.toml (name, extensions, filenames, shebangs),
// grammar.wasm and queries/*.scm. Its name is the directory's unless language.toml names it.
func ReadLanguagePack(dir string) (registry.Language, error) {
	l := registry.Language{Name: filepath.Base(dir), Source: dir, Queries: map[string]string{}}
	if src, err := os.ReadFile(filepath.Join(dir, "language.toml")); err == nil {
		doc, err := toml.Parse(src)
		if err != nil {
			return l, err
		}
		v := doc.Value()
		if n := tstr(v["name"]); n != "" {
			l.Name = n
		}
		l.Extensions, l.Filenames, l.Shebangs = Strings(v["extensions"]), Strings(v["filenames"]), Strings(v["shebangs"])
	} else if !errors.Is(err, fs.ErrNotExist) {
		return l, err
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

// Asset reads one of the plugin's assets: name is under its assets/ directory, and may not leave it.
func Asset(dir, name string) ([]byte, error) {
	if !local(name) {
		return nil, fmt.Errorf("asset %q isn't a path under %s/", name, AssetsDir)
	}
	f := filepath.Join(dir, AssetsDir, filepath.FromSlash(name))
	root := filepath.Join(dir, AssetsDir)
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("asset %s: the plugin has no %s/: %w", name, AssetsDir, fs.ErrNotExist)
	}
	real, err := filepath.EvalSymlinks(f)
	if err != nil {
		return nil, fmt.Errorf("asset %s: %w", name, err)
	}
	if !Within(r, real) {
		return nil, fmt.Errorf("asset %q leads out of %s/", name, AssetsDir)
	}
	return os.ReadFile(real)
}

// Size is how much the files under dir take, unpacked, .git left out.
func Size(dir string) (int64, error) {
	var n int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			i, err := d.Info()
			if err != nil {
				return err
			}
			n += i.Size()
		}
		return nil
	})
	return n, err
}
