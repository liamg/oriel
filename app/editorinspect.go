package app

import (
	"fmt"
	"strings"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
)

// inspectSections are the editor's parts of :Inspect (20-inspect): the syntax tree and highlight under the cursor,
// and the decorations on its line. The language server's section is lsp's.
func (x *editorExt) inspectSections() []registry.InspectSection {
	return []registry.InspectSection{
		{Name: "TREE-SITTER", Order: 10, Rows: x.inspectTree, Source: "builtin:editor"},
		{Name: "HIGHLIGHT", Order: 20, Rows: x.inspectHighlight, Source: "builtin:editor"},
		{Name: "DECORATIONS", Order: 40, Rows: x.inspectDecorations, Source: "builtin:editor"},
	}
}

// cursorOffset is the cursor's byte offset in the current buffer, and where that is.
func (x *editorExt) cursorOffset() (int, string) {
	p, ok := x.e.Pane(x.e.WindowID())
	if !ok {
		return 0, ""
	}
	snap := p.Buffer().Snapshot()
	c := p.Cursor()
	return min(snap.LineStart(c.Line)+c.Col, snap.Len()), fmt.Sprintf("%s %d:%d", p.Name(), c.Line+1, c.Col+1)
}

func (x *editorExt) inspectTree() []registry.InspectRow {
	p, ok := x.e.Pane(x.e.WindowID())
	if !ok {
		return nil
	}
	h := x.highlights(p)
	if h.doc == nil {
		return []registry.InspectRow{{Key: "—", Value: "no grammar for " + p.Name(), Faded: true}}
	}
	off, at := x.cursorOffset()
	return []registry.InspectRow{{Key: at, Value: strings.Join(h.doc.NodePath(off), " › "), Source: h.lang}}
}

func (x *editorExt) inspectHighlight() []registry.InspectRow {
	p, ok := x.e.Pane(x.e.WindowID())
	if !ok {
		return nil
	}
	h := x.highlights(p)
	if h.doc == nil {
		return nil
	}
	off, _ := x.cursorOffset()
	spans, _ := h.doc.Highlights(off, off+1)
	th := x.h.CurrentTheme()
	var out []registry.InspectRow
	for _, s := range spans {
		val := ""
		if th != nil {
			val = theme.Hex(th.Syntax("@" + s.Capture).FG)
		}
		src := "theme"
		if th != nil {
			src = "theme " + th.Name
		}
		out = append(out, registry.InspectRow{Key: "@" + s.Capture, Value: val, Source: src})
	}
	return out
}

func (x *editorExt) inspectDecorations() []registry.InspectRow {
	p, ok := x.e.Pane(x.e.WindowID())
	if !ok {
		return nil
	}
	snap := p.Buffer().Snapshot()
	line := p.Cursor().Line
	ctx := registry.DecorationCtx{Name: p.Name(), Buffer: p.Buffer(), Snap: snap, Top: line, Bottom: line + 1}
	kinds := []string{"sign", "line", "highlight", "underline", "virtual text", "block"}
	var out []registry.InspectRow
	for _, prov := range x.h.DecorationProviders() {
		var ds []registry.Decoration
		if registry.Call(func() error { ds = prov.Decorations(ctx); return nil }) != nil {
			continue
		}
		for _, d := range ds {
			if d.Line != line {
				continue
			}
			k := "?"
			if int(d.Kind) < len(kinds) {
				k = kinds[d.Kind]
			}
			out = append(out, registry.InspectRow{Key: prov.ID, Value: strings.TrimSpace(k + " " + d.Text), Source: d.Style})
		}
	}
	return out
}
