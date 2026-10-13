package registry

import (
	"cmp"
	"slices"
)

// Diagnostic severities, as LSP's.
const (
	SevError   = "error"
	SevWarning = "warning"
	SevInfo    = "info"
	SevHint    = "hint"
)

// Diagnostic is a problem at a place in a file: a Lua error, a language server's, a linter's. The editor marks it
// on its line and Problems lists it.
type Diagnostic struct {
	File     string // absolute
	Line     int    // 0-based
	From, To int    // byte columns; To 0 is the end of the line
	Severity string
	Message  string
	Owner    string // who published it: "lua", "gopls"
}

type diagKey struct{ owner, file string }

func (r *reg) SetDiagnostics(owner, file string, ds []Diagnostic) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.diags == nil {
		r.diags = map[diagKey][]Diagnostic{}
	}
	k := diagKey{owner, file}
	if len(ds) == 0 {
		if _, ok := r.diags[k]; !ok {
			return
		}
		delete(r.diags, k)
	} else {
		out := make([]Diagnostic, len(ds))
		for i, d := range ds {
			d.File, d.Owner = file, owner
			out[i] = d
		}
		r.diags[k] = out
	}
	r.diagGen++
}

func (r *reg) Diagnostics() ([]Diagnostic, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Diagnostic
	for _, ds := range r.diags {
		out = append(out, ds...)
	}
	sortDiagnostics(out)
	return out, r.diagGen
}

func (r *reg) DiagnosticsFor(file string) []Diagnostic {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Diagnostic
	for k, ds := range r.diags {
		if k.file == file {
			out = append(out, ds...)
		}
	}
	sortDiagnostics(out)
	return out
}

func sortDiagnostics(ds []Diagnostic) {
	slices.SortStableFunc(ds, func(a, b Diagnostic) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line), cmp.Compare(a.From, b.From), cmp.Compare(a.Owner, b.Owner))
	})
}
