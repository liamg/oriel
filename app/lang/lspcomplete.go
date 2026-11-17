package lang

import (
	"context"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit/fuzzy"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/text"
)

// requestCompletion asks the server for the items at the cursor. Asked for by hand, it says when there are none, or
// why the server gave none; as you type, an empty answer just leaves the menu shut.
func (x *LSP) requestCompletion(d *lspDoc, manual bool) {
	p := window(x.h)
	pos := p.Cursor
	line := p.Text.Snapshot().Line(pos.Line)
	col := min(pos.Col, len(line))
	x.gen++
	gen := x.gen
	c := d.client
	async(x, func(ctx context.Context) (*lsp.CompletionList, error) {
		return c.Complete(ctx, lsp.Loc{Path: d.path, Pos: text.Pos{Line: pos.Line, Col: col}})
	}, func(list *lsp.CompletionList, err error) {
		if gen != x.gen {
			return
		}
		none := func(why string) {
			if manual {
				x.h.Notify(core.Notice{ID: "lsp.complete", Severity: core.SevInfo, AutoClear: true, Source: "lsp", Text: why})
			}
		}
		if err != nil {
			none("No completions: " + err.Error())
			return
		}
		p := window(x.h)
		now := p.Cursor
		if list == nil || p.Text == nil || now.Line != pos.Line || editorMode(x.h) != "insert" {
			if list == nil {
				none("No completions")
			}
			return
		}
		ln := p.Text.Snapshot().Line(now.Line)
		start := wordStart(ln, min(col, len(ln)))
		x.comp = completion{path: d.path, line: pos.Line, start: start, items: list.Items, resolved: map[int]lsp.Completion{}, gen: gen}
		x.filter(ln[start:min(now.Col, len(ln))])
		if !x.comp.open {
			none("No completions")
		}
	})
}

// filter ranks the items against the word typed so far, and opens the menu while any match.
func (x *LSP) filter(word []byte) {
	type hit struct{ i, score int }
	var hits []hit
	for i, it := range x.comp.items {
		ft := it.FilterText
		if ft == "" {
			ft = it.Label
		}
		if len(word) == 0 {
			hits = append(hits, hit{i, 0})
			continue
		}
		if score, _, ok := fuzzy.Match(string(word), ft); ok {
			hits = append(hits, hit{i, score})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return strings.Compare(x.comp.items[a.i].SortText, x.comp.items[b.i].SortText)
	})
	x.comp.shown = x.comp.shown[:0]
	for _, h := range hits {
		x.comp.shown = append(x.comp.shown, h.i)
	}
	x.comp.sel = 0
	x.comp.open = len(x.comp.shown) > 0
}

// selected is the highlighted item, resolved if it has been.
func (x *LSP) selected() (lsp.Completion, int, bool) {
	if !x.comp.open || x.comp.sel >= len(x.comp.shown) {
		return lsp.Completion{}, 0, false
	}
	i := x.comp.shown[x.comp.sel]
	if r, ok := x.comp.resolved[i]; ok {
		return r, i, true
	}
	return x.comp.items[i], i, true
}
