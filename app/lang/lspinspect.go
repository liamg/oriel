package lang

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/registry"
)

// inspectRows are :Inspect's language server section: the server for the file, and the semantic token under the
// cursor, asked for the first time it's shown after an edit.
func (x *LSP) inspectRows() []registry.InspectRow {
	p, path, d := x.current()
	faded := func(s string) []registry.InspectRow {
		return []registry.InspectRow{{Key: "—", Value: s, Faded: true}}
	}
	switch {
	case path == "":
		return nil
	case d == nil:
		return faded("no language server for " + filepath.Base(path))
	case d.opening:
		return faded("starting the " + d.lang + " server…")
	case d.client == nil:
		return faded("the " + d.lang + " server didn't start: :LspInfo says why")
	}
	st := d.client.Status()
	rows := []registry.InspectRow{{Key: "server", Value: strings.TrimSpace(st.Server + " " + st.Version), Source: st.State.String()}}
	snap := p.Text.Snapshot()
	if d.tokensVersion != snap.Version()+1 && !d.tokensLoading {
		d.tokensLoading = true
		version, c := snap.Version(), d.client
		async(x, func(ctx context.Context) ([]lsp.SemanticToken, error) { return c.SemanticTokens(ctx, d.path) },
			func(ts []lsp.SemanticToken, err error) {
				d.tokensLoading = false
				if err == nil || errors.Is(err, lsp.ErrNotSupported) {
					d.tokens, d.tokensVersion = ts, version+1
				}
				x.h.Redraw()
			})
	}
	if d.tokensVersion != snap.Version()+1 {
		return append(rows, registry.InspectRow{Key: "token", Value: "waiting for " + st.Server, Faded: true})
	}
	cur := p.Cursor
	for _, t := range d.tokens {
		r := t.Range
		if r.Start.Line == cur.Line && r.Start.Col <= cur.Col && cur.Col < r.End.Col {
			return append(rows, registry.InspectRow{Key: "token", Value: strings.TrimSpace(t.Type + " " + strings.Join(t.Modifiers, " ")),
				Source: st.Server})
		}
	}
	return append(rows, registry.InspectRow{Key: "token", Value: "none under the cursor", Faded: true})
}
