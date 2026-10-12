package syntax

import (
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Language is a tree-sitter grammar, built to WASM with grammars/shim.c, and its queries.
// Built-in languages are registered the same way a plugin registers its own.
type Language struct {
	Name       string
	Wasm       []byte
	Extensions []string          // file extensions, with the dot: ".go"
	Queries    map[string]string // by kind: "highlights", "folds", "injections", "units"
}

var languages struct {
	sync.RWMutex
	byName map[string]*Language
}

// Register adds a language, replacing any of the same name.
func Register(l *Language) {
	languages.Lock()
	defer languages.Unlock()
	if languages.byName == nil {
		languages.byName = map[string]*Language{}
	}
	languages.byName[l.Name] = l
}

// Lookup returns a registered language by name.
func Lookup(name string) (*Language, bool) {
	languages.RLock()
	defer languages.RUnlock()
	l, ok := languages.byName[name]
	return l, ok
}

// ForPath returns the language for a file, by its extension.
func ForPath(path string) (*Language, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	languages.RLock()
	defer languages.RUnlock()
	for _, l := range languages.byName {
		for _, e := range l.Extensions {
			if e == ext {
				return l, true
			}
		}
	}
	return nil, false
}

var inheritsLine = regexp.MustCompile(`^;+\s*inherits\s*:?\s*([\w,]+)`)

// query returns a language's query of a kind, with any languages it inherits from (an "; inherits: ecma"
// first line, as nvim-treesitter writes them) put in front.
func (l *Language) query(kind string) string {
	src := l.Queries[kind]
	m := inheritsLine.FindStringSubmatch(src)
	if m == nil {
		return src
	}
	var b strings.Builder
	for _, name := range strings.Split(m[1], ",") {
		if base, ok := Lookup(name); ok && base != l {
			b.WriteString(base.query(kind))
			b.WriteByte('\n')
		} else if q, ok := inheritedOnly[name]; ok {
			b.WriteString(q[kind])
			b.WriteByte('\n')
		}
	}
	b.WriteString(src)
	return b.String()
}

// inheritedOnly holds query sets that exist only to be inherited, like nvim-treesitter's "ecma".
var inheritedOnly = map[string]map[string]string{}
