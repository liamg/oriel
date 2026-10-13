package syntax

import (
	"bytes"
	"crypto/sha256"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Language is a tree-sitter grammar, built to WASM with grammars/shim.c, its queries, and the files it's for. One
// with no grammar is a set of queries other languages inherit, like nvim-treesitter's "ecma". Languages come in a
// Set: the IDE's is the registry's language contributions (ED-11).
type Language struct {
	Name       string
	Extensions []string               // with the dot: ".go"
	Filenames  []string               // whole names: "Makefile", "Dockerfile"
	Shebangs   []string               // interpreters a #! line names: "python3", "bash"
	Grammar    func() ([]byte, error) // the WASM, read when a parser first needs it
	Queries    map[string]string      // by kind: "highlights", "folds", "injections", "units"

	set  *Set
	once sync.Once
	wasm []byte
	sum  [32]byte
	err  error
}

// load reads the grammar once, and its checksum, which compiled modules are cached by.
func (l *Language) load() ([]byte, [32]byte, error) {
	l.once.Do(func() {
		if l.Grammar == nil {
			l.err = errNoGrammar(l.Name)
			return
		}
		l.wasm, l.err = l.Grammar()
		l.sum = sha256.Sum256(l.wasm)
	})
	return l.wasm, l.sum, l.err
}

type errNoGrammar string

func (e errNoGrammar) Error() string { return "syntax: " + string(e) + " has queries but no grammar" }

// Set is languages to look up by name and by file. Later ones win: a language registered after another of the same
// name shadows it, and one claiming the same files is chosen first.
type Set struct {
	langs []*Language
}

// NewSet is a set of ls, in order. A language looks the languages it names up in the first set it joins.
func NewSet(ls ...*Language) *Set {
	s := &Set{langs: ls}
	for _, l := range ls {
		if l.set == nil {
			l.set = s
		}
	}
	return s
}

// Language finds a language by name.
func (s *Set) Language(name string) (*Language, bool) {
	for i := len(s.langs) - 1; i >= 0; i-- {
		if s.langs[i].Name == name {
			return s.langs[i], true
		}
	}
	return nil, false
}

// ForFile finds the language for a file: by its whole name, then its extension, then the interpreter its first
// line's #! names.
func (s *Set) ForFile(path string, firstLine []byte) (*Language, bool) {
	base := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(path))
	interp := shebang(firstLine)
	for _, match := range []func(l *Language) bool{
		func(l *Language) bool { return slices.Contains(l.Filenames, base) },
		func(l *Language) bool { return ext != "" && slices.Contains(l.Extensions, ext) },
		func(l *Language) bool {
			return interp != "" && (slices.Contains(l.Shebangs, interp) || slices.Contains(l.Shebangs, strings.TrimRight(interp, "0123456789.")))
		},
	} {
		for i := len(s.langs) - 1; i >= 0; i-- {
			if l := s.langs[i]; l.Grammar != nil && match(l) {
				return l, true
			}
		}
	}
	return nil, false
}

// shebang is the interpreter a "#!" line runs: its program, or the one env runs.
func shebang(line []byte) string {
	line, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("#!"))
	if !ok {
		return ""
	}
	f := strings.Fields(string(line))
	if len(f) == 0 {
		return ""
	}
	prog := filepath.Base(f[0])
	if prog == "env" {
		for _, a := range f[1:] {
			if !strings.HasPrefix(a, "-") {
				return filepath.Base(a)
			}
		}
		return ""
	}
	return prog
}

// lookup finds a language another one names (one it inherits queries from, or injects) in its set, or among the
// built-ins when it's in none.
func (l *Language) lookup(name string) (*Language, bool) {
	if l.set != nil {
		return l.set.Language(name)
	}
	return Builtin().Language(name)
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
		if base, ok := l.lookup(name); ok && base != l {
			b.WriteString(base.query(kind))
			b.WriteByte('\n')
		}
	}
	b.WriteString(src)
	return b.String()
}
