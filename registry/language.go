package registry

import (
	"slices"
	"strings"
)

// Language is a language contribution (ED-11): a tree-sitter grammar built to WASM and its queries, and the files
// it's for. A later registration of a name shadows the earlier until it's disposed; one with no Grammar is queries
// for others to inherit.
type Language struct {
	Name       string
	Extensions []string               // with the dot: ".go"
	Filenames  []string               // whole names: "Makefile"
	Shebangs   []string               // interpreters a #! line names: "python3"
	Grammar    func() ([]byte, error) // the WASM, read when it's first needed
	Queries    map[string]string      // by kind: "highlights", "folds", "injections", "units"
	Source     string                 // who registered it: "builtin:languages", a pack's directory
}

func (r *reg) Language(l Language) Disposable {
	r.mu.Lock()
	defer r.mu.Unlock()
	tok := r.languages.push(l.Name, l)
	r.langGen++
	return disposeOnce(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.languages.remove(l.Name, tok)
		r.langGen++
	})
}

func (r *reg) Languages() ([]Language, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	type winner struct {
		tok int64
		l   Language
	}
	var ws []winner
	for _, es := range r.languages {
		e := es[len(es)-1]
		ws = append(ws, winner{e.token, e.value})
	}
	slices.SortFunc(ws, func(a, b winner) int {
		if a.tok != b.tok {
			return int(a.tok - b.tok)
		}
		return strings.Compare(a.l.Name, b.l.Name)
	})
	out := make([]Language, len(ws))
	for i, w := range ws {
		out[i] = w.l
	}
	return out, r.langGen
}
