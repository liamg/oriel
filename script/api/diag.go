package api

import (
	"cmp"
	"fmt"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

//oriel:api name=diagnostics
type Diagnostics struct{ a *APIs }

//oriel:api doc="Set owner's diagnostics for a file, replacing its last: a list of { line, col, end_col, severity (error, warning, info, hint), message }, 1-based. An empty list clears them; so does reloading the config."
func (d Diagnostics) Set(owner, file string, list any) error {
	file = expand(file)
	items, _ := list.([]any)
	var ds []registry.Diagnostic
	for _, it := range items {
		t, ok := it.(script.Table)
		if !ok {
			return fmt.Errorf("a diagnostic is a table, not %T", it)
		}
		line, _ := t["line"].(float64)
		col, _ := t["col"].(float64)
		end, _ := t["end_col"].(float64)
		ds = append(ds, registry.Diagnostic{Line: int(line) - 1, From: max(0, int(col)-1), To: max(0, int(end)-1),
			Severity: cmpOr(str(t, "severity"), registry.SevError), Message: str(t, "message")})
	}
	d.a.Host.SetDiagnostics(owner, file, ds)
	d.a.own(disposeFunc(func() { d.a.Host.SetDiagnostics(owner, file, nil) }))
	return nil
}

//oriel:api doc="Every diagnostic, each { file, line, col, end_col, severity, message, owner }, 1-based." returns=table
func (d Diagnostics) List() []any {
	ds, _ := d.a.Host.Diagnostics()
	out := make([]any, 0, len(ds))
	for _, x := range ds {
		out = append(out, script.Table{"file": x.File, "line": x.Line + 1, "col": x.From + 1, "end_col": x.To + 1,
			"severity": x.Severity, "message": x.Message, "owner": x.Owner})
	}
	return out
}

type disposeFunc func()

func (f disposeFunc) Dispose() { f() }

// cmpOr is the first of ss that isn't empty.
func cmpOr(ss ...string) string { return cmp.Or(ss...) }
