package kit

import (
	"bytes"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/syntax"
)

// LanguagesService is the window's languages as one syntax.Set: a LanguageSet, which the editor's languages
// plugin keeps.
const LanguagesService = "kit.languages"

// LanguageSet is the window's registered languages as a syntax.Set, made again when they change.
type LanguageSet interface{ Set() *syntax.Set }

// WindowRegistry is the registry r stands for: a plugin's host is a view of its window's.
func WindowRegistry(r registry.Registry) registry.Registry {
	if h, ok := r.(interface{ WindowRegistry() registry.Registry }); ok {
		if w := h.WindowRegistry(); w != nil {
			return w
		}
	}
	return r
}

// Languages is the set of languages r has now: the window's kept set, else one made now, closed so it keeps no
// idle parsers (nothing would close them).
func Languages(r registry.Registry) *syntax.Set {
	if s, ok := registry.ServiceOf[LanguageSet](r, LanguagesService); ok {
		return s.Set()
	}
	s := LanguageSetFrom(r)
	s.Close()
	return s
}

// LanguageSetFrom makes r's languages into a syntax.Set; its owner closes it.
func LanguageSetFrom(r registry.Registry) *syntax.Set {
	ls := r.Languages()
	var out []*syntax.Language
	for _, l := range ls {
		out = append(out, &syntax.Language{Name: l.Name, Extensions: l.Extensions, Filenames: l.Filenames, Shebangs: l.Shebangs,
			Grammar: l.Grammar, Queries: l.Queries})
	}
	return syntax.NewSet(out...)
}

// LanguageFor is the language of the file at path, by its name or the first line of src.
func LanguageFor(r registry.Registry, path string, src []byte) (*syntax.Language, bool) {
	first, _, _ := bytes.Cut(src[:min(len(src), 512)], []byte("\n"))
	return Languages(r).ForFile(path, first)
}
