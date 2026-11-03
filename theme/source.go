package theme

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Source is where a theme comes from: a built-in, a file, a plugin, a Lua-generated theme.
type Source interface {
	Name() string
	Load() (*Spec, error)
	Watch(fn func()) (cancel func()) // fn runs when the theme may have changed
}

//go:embed themes/*.toml
var builtinFS embed.FS

type builtin struct{ name, file string }

// Builtins are the themes shipped with Oriel.
func Builtins() []Source {
	files, _ := fs.Glob(builtinFS, "themes/*.toml")
	out := make([]Source, len(files))
	for i, f := range files {
		out[i] = builtin{strings.TrimSuffix(path.Base(f), ".toml"), f}
	}
	return out
}

func (b builtin) Name() string { return b.name }

func (b builtin) Load() (*Spec, error) {
	src, err := builtinFS.ReadFile(b.file)
	if err != nil {
		return nil, err
	}
	s, err := ParseSpec(src)
	if err != nil {
		return nil, fmt.Errorf("theme %s: %w", b.name, err)
	}
	if s.Name == "" {
		s.Name = b.name
	}
	return s, nil
}

func (builtin) Watch(func()) func() { return func() {} }

// File is a theme file, named by its name key or else its file name. Watch polls it, so a save shows
// within PollEvery.
type File struct {
	Path      string
	PollEvery time.Duration
}

func (f File) Name() string {
	if s, err := f.Load(); err == nil {
		return s.Name
	}
	return strings.TrimSuffix(filepath.Base(f.Path), filepath.Ext(f.Path))
}

func (f File) Load() (*Spec, error) {
	src, err := os.ReadFile(f.Path)
	if err != nil {
		return nil, err
	}
	s, err := ParseSpec(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Path, err)
	}
	if s.Name == "" {
		s.Name = strings.TrimSuffix(filepath.Base(f.Path), filepath.Ext(f.Path))
	}
	return s, nil
}

func (f File) Watch(fn func()) (cancel func()) {
	every := f.PollEvery
	if every == 0 {
		every = 250 * time.Millisecond
	}
	// Contents, not mtimes: two same-sized saves can share an mtime tick.
	read := func() [32]byte {
		b, err := os.ReadFile(f.Path)
		if err != nil {
			return [32]byte{}
		}
		return sha256.Sum256(b)
	}
	last := read()
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if h := read(); h != last {
					last = h
					fn()
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// Stack loads the theme called name and everything it extends, base first.
func Stack(sources []Source, name string) ([]*Spec, error) {
	byName := map[string]Source{}
	for _, s := range sources {
		byName[s.Name()] = s
	}
	var out []*Spec
	seen := map[string]bool{}
	for name != "" {
		if seen[name] {
			return nil, fmt.Errorf("theme %s extends itself", name)
		}
		seen[name] = true
		src, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("no theme %q", name)
		}
		s, err := src.Load()
		if err != nil {
			return nil, err
		}
		out = append([]*Spec{s}, out...)
		name = s.Extends
	}
	return out, nil
}
