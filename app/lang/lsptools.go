package lang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/text"
)

// agentTools offers the language servers to agents, read-only: where a symbol is defined, what refers to
// it, and a file's diagnostics. They're ordinary agent tools, as a plugin's would be.
func (x *LSP) agentTools(h plugin.Host) {
	at := map[string]string{"file": "string", "line": "number", "column": "number?", "symbol": "string?"}
	for _, t := range []registry.AgentTool{
		{Name: "lsp_definition", ReadOnly: true, Params: at, Run: x.toolLocations("definition", (*lsp.Client).Definition),
			Desc: "Where a symbol is defined, from the language server. file is a path (relative to the session's project, or absolute); line is 1-based; give column (1-based) or the symbol's name on that line."},
		{Name: "lsp_references", ReadOnly: true, Params: at, Run: x.toolLocations("references", func(c *lsp.Client, ctx context.Context, l lsp.Loc) ([]lsp.Location, error) {
			return c.References(ctx, l, true)
		}), Desc: "Every reference to a symbol, from the language server, its declaration included. Same arguments as lsp_definition."},
		{Name: "lsp_diagnostics", ReadOnly: true, Params: map[string]string{"file": "string?"}, Run: x.toolDiagnostics,
			Desc: "The language servers' errors and warnings: for a file, or every file they've reported on when file is empty."},
	} {
		t.Source = "builtin:lsp"
		x.Add(h.Contribute(t))
	}
}

func (x *LSP) agentPath(ctx registry.Ctx, file string) (string, error) {
	return agentPath(x.h, x.dir(), ctx, file)
}

// agentPath is an agent tool's file argument as an absolute path: relative to the asking session's project, or the
// window's, root.
func agentPath(h plugin.Host, root string, ctx registry.Ctx, file string) (string, error) {
	if file == "" {
		return "", errors.New("no file given")
	}
	if filepath.IsAbs(file) {
		return filepath.Clean(file), nil
	}
	if se, ok := h.Session(core.SessionID(registry.SessionOf(ctx))); ok {
		if dir := kit.SessionDir(se.Snapshot()); dir != "" {
			root = dir
		}
	}
	if root == "" {
		return "", errors.New("give an absolute path: there's no project to be relative to")
	}
	return filepath.Join(root, file), nil
}

func (x *LSP) onLoop(ctx context.Context, fn func()) error { return kit.OnLoop(ctx, x.h, fn) }

// agentDoc is the server's client for path, opening the file on it from disk when no editor has; release closes
// what it opened.
func (x *LSP) agentDoc(ctx context.Context, path string) (c *lsp.Client, src []byte, release func(), err error) {
	release = func() {}
	if err := x.onLoop(ctx, func() { src = x.fileText(path) }); err != nil {
		return nil, nil, release, err
	}
	if src == nil {
		if _, err := os.Stat(path); err != nil {
			return nil, nil, release, err
		}
	}
	if c, ok := x.m.Client(path); ok {
		return c, src, release, nil
	}
	lang, ok := kit.LanguageFor(x.h, path, src)
	if !ok {
		return nil, nil, release, fmt.Errorf("no language for %s", filepath.Base(path))
	}
	c, err = x.m.Open(ctx, path, lang.Name, text.New(src, text.Author{}).Snapshot())
	if err != nil {
		if errors.Is(err, lsp.ErrNoServer) {
			err = fmt.Errorf("no language server for %s files", lang.Name)
		}
		return nil, nil, release, err
	}
	return c, src, func() {
		x.mu.Lock()
		_, editing := x.docs[path]
		x.mu.Unlock()
		if !editing {
			x.m.Close(path)
		}
	}, nil
}

// position is a tool's line and column (or symbol) as a server position.
func position(src []byte, p map[string]any) (text.Pos, error) {
	line := intArg(p["line"])
	lines := strings.Split(string(src), "\n")
	if line < 1 || line > len(lines) {
		return text.Pos{}, fmt.Errorf("line %d is outside the file's %d lines", line, len(lines))
	}
	l := lines[line-1]
	if sym, _ := p["symbol"].(string); sym != "" {
		i := strings.Index(l, sym)
		if i < 0 {
			return text.Pos{}, fmt.Errorf("%q isn't on line %d: %s", sym, line, strings.TrimSpace(l))
		}
		return text.Pos{Line: line - 1, Col: i}, nil
	}
	col := intArg(p["column"])
	if col < 1 {
		return text.Pos{}, errors.New("give a column or the symbol's name")
	}
	return text.Pos{Line: line - 1, Col: min(col-1, len(l))}, nil
}

func intArg(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		var i int
		fmt.Sscanf(n, "%d", &i)
		return i
	}
	return 0
}

func (x *LSP) toolLocations(what string, find func(*lsp.Client, context.Context, lsp.Loc) ([]lsp.Location, error)) func(registry.Ctx, map[string]any) (any, error) {
	return func(ctx registry.Ctx, p map[string]any) (any, error) {
		file, _ := p["file"].(string)
		path, err := x.agentPath(ctx, file)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		c, src, release, err := x.agentDoc(ctx, path)
		defer release()
		if err != nil {
			return nil, err
		}
		pos, err := position(src, p)
		if err != nil {
			return nil, err
		}
		ls, err := find(c, ctx, lsp.Loc{Path: path, Pos: pos})
		if err != nil {
			return nil, err
		}
		if len(ls) == 0 {
			return "no " + what + " found", nil
		}
		var b strings.Builder
		for _, l := range ls {
			fmt.Fprintf(&b, "%s:%d:%d\t%s\n", x.agentRel(l.Path), l.Range.Start.Line+1, l.Range.Start.Col+1, lineOf(x, l))
		}
		return strings.TrimSpace(b.String()), nil
	}
}

func (x *LSP) agentRel(path string) string { return agentRel(x.dir(), path) }

// agentRel is a path as an agent gives it: relative to the project, root, when it's inside it.
func agentRel(root, path string) string {
	if root != "" {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return path
}

func (x *LSP) toolDiagnostics(ctx registry.Ctx, p map[string]any) (any, error) {
	var ds []registry.Diagnostic
	if file, _ := p["file"].(string); file != "" {
		path, err := x.agentPath(ctx, file)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		_, _, release, err := x.agentDoc(ctx, path)
		defer release()
		if err != nil {
			return nil, err
		}
		// A file just opened gets its diagnostics a moment later.
		deadline := time.Now().Add(3 * time.Second)
		for {
			if ds = x.h.DiagnosticsFor(path); len(ds) > 0 || time.Now().After(deadline) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	} else {
		ds, _ = x.h.Diagnostics()
	}
	if len(ds) == 0 {
		return "no diagnostics", nil
	}
	var b strings.Builder
	for _, d := range ds {
		fmt.Fprintf(&b, "%s:%d:%d\t%s\t%s\n", x.agentRel(d.File), d.Line+1, d.From+1, d.Severity, d.Message)
	}
	return strings.TrimSpace(b.String()), nil
}
