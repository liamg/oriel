package api

import (
	"fmt"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/script"
)

//oriel:api name=explorer cap=ui
type Explorer struct{ a *APIs }

//oriel:api doc="Mark files in the Explorer: { name, glyph, tone (a theme token), noun (what's counted: 'comment' makes the tooltip '3 comments'), order (50 by default), counts = fn(session) returning { [absolute path] = count } }. A file's mark is the glyph and its count; a folder's sums its files'. counts is called once a frame while the Explorer shows: keep it cheap."
func (x Explorer) Badge(spec script.Table, where string) error {
	b := uireg.FileBadge{Name: str(spec, "name"), Glyph: str(spec, "glyph"), Tone: str(spec, "tone"), Noun: str(spec, "noun"), Order: 50, Source: where}
	fn, ok := spec["counts"].(*script.Fn)
	if b.Name == "" || !ok {
		return fmt.Errorf("an Explorer badge needs a name and counts")
	}
	if o, ok := spec["order"].(float64); ok {
		b.Order = int(o)
	}
	b.Counts = func(id core.SessionID) map[string]int {
		out, err := x.a.VM.Call(fn, string(id))
		if err != nil || len(out) == 0 {
			return nil
		}
		t, _ := out[0].(script.Table)
		counts := map[string]int{}
		for p, n := range t {
			if k := int(num(n)); k > 0 {
				counts[p] = k
			}
		}
		return counts
	}
	x.a.own(x.a.Host.Contribute(b))
	return nil
}
