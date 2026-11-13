package api

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

//oriel:api name=diagnostics cap=ui
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
	d.a.own(registry.DisposeFunc(func() { d.a.Host.SetDiagnostics(owner, file, nil) }))
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

// cmpOr is the first of ss that isn't empty.
func cmpOr(ss ...string) string { return cmp.Or(ss...) }

//oriel:api name=coverage cap=ui
type Coverage struct{ a *APIs }

//oriel:api doc="Set owner's line coverage for a file, replacing its last: { [line] = hits }, 1-based, 0 for a line never run. The tests gutter shows it; an empty table clears it, and so does reloading the config."
func (c Coverage) Set(owner, file string, hits any) error {
	file = expand(file)
	t, _ := hits.(script.Table)
	if list, ok := hits.([]any); ok {
		t = script.Table{}
		for i, v := range list {
			if v != nil {
				t[strconv.Itoa(i+1)] = v
			}
		}
	}
	m := map[int]int{}
	for k, v := range t {
		line, err := strconv.Atoi(k)
		n, ok := v.(float64)
		if err != nil || !ok || line < 1 {
			return fmt.Errorf("coverage is { [line] = hits }, not %s = %v", k, v)
		}
		m[line-1] = int(n)
	}
	c.a.Host.SetCoverage(owner, file, m)
	c.a.own(registry.DisposeFunc(func() { c.a.Host.SetCoverage(owner, file, nil) }))
	return nil
}

//oriel:api doc="A file's measured lines from first to last, each { n, hits, hit }, 1-based, from every owner." returns=table
func (c Coverage) Lines(file string, first, last int) []any {
	hits := c.a.Host.CoverageFor(expand(file))
	var out []any
	for _, l := range slices.Sorted(maps.Keys(hits)) {
		if l+1 >= first && l+1 <= last {
			out = append(out, script.Table{"n": l + 1, "hits": hits[l], "hit": hits[l] > 0})
		}
	}
	return out
}
