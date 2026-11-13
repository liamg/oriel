package gitui

import (
	"context"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// scratchID heads the scratchpad's rows: its root, and before each of its paths.
const scratchID = "\x03"

func (e *Explorer) watchScratch() {
	e.scratch = kit.NewDirTree(e.h)
	e.Add(kit.OnDispose(e.scratch.Close))
	e.pads = plugin.NewCache(e.h, func(ctx context.Context, id core.SessionID) (string, error) { return scratchpadOf(ctx, e.h, id) },
		plugin.CacheOpts[core.SessionID, string]{MaxEntries: 16})
	e.Add(kit.OnDispose(e.pads.Close))
}

// scratchRoot is the scratchpad's row in All, its folders under it, when the session in front has one.
func (e *Explorer) scratchRoot(c *ui.Ctx, sc gitx.Scope) (widget.TreeItem, bool) {
	if sc != gitx.All {
		return widget.TreeItem{}, false
	}
	pad, _ := e.pads.Get(e.session())
	if pad == "" {
		return widget.TreeItem{}, false
	}
	kids, _ := e.scratch.Items(pad, scratchID)
	return widget.TreeItem{ID: scratchID, Label: "scratchpad", Note: "the agent's temp dir", Swatch: c.Theme().C(theme.FgFaint), Open: true,
		Children: kids, Help: "files/explorer#scratchpad"}, true
}

// openScratch opens a scratchpad row's file.
func (e *Explorer) openScratch(id string) {
	if p := e.scratch.Path(id, scratchID); p != "" {
		if err := e.h.Run(context.Background(), "editor.open", registry.Args{"path": p}); err != nil {
			e.h.Notify(core.Notice{ID: "explorer.open", Severity: core.SevWarning, Key: true, Text: err.Error()})
		}
	}
}

// scratchpadOf is where session id's agent keeps its scratch files on this machine: a sandboxed agent's temp dir.
func scratchpadOf(ctx context.Context, h plugin.Host, id core.SessionID) (string, error) {
	w, err := kit.SessionCalls(h)
	if err != nil || id == "" {
		return "", nil
	}
	ws, err := w.Workspace(ctx, id)
	if err != nil {
		return "", err
	}
	for _, p := range ws.Paths {
		if p.Name == "Scratchpad" {
			return p.Host, nil
		}
	}
	return "", nil
}
