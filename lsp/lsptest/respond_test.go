package lsptest_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/lsp/lsptest"
	"github.com/liamg/oriel/text"
)

// The responder answers what it's asked from the transcript, in any order, and sends a document's diagnostics when
// it's opened.
func TestResponder(t *testing.T) {
	tr, err := lsptest.Load("../testdata/transcripts/go.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	for _, f := range []string{"go.mod", "lib.go", "main.go.in"} {
		b, _ := os.ReadFile(filepath.Join("../testdata/go", f))
		os.WriteFile(filepath.Join(root, strings.TrimSuffix(f, ".in")), b, 0o644)
	}
	r := lsptest.NewResponder(tr)
	diags := make(chan lsp.DiagnosticSet, 10)
	var spec lsp.ServerSpec
	for _, s := range lsp.Builtins() {
		if s.Name == "gopls" {
			spec = s
		}
	}
	m := lsp.NewManager(lsp.ManagerOptions{Servers: func() []lsp.ServerSpec { return []lsp.ServerSpec{spec} },
		Installed: func(lsp.ServerSpec) bool { return true },
		Client:    lsp.Options{Launch: r.Launcher(), OnDiagnostics: func(s lsp.DiagnosticSet) { diags <- s }}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	main := filepath.Join(root, "main.go")
	b, _ := os.ReadFile(main)
	c, err := m.Open(ctx, main, "go", text.New(b, text.Author{}).Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Hover(ctx, lsp.Loc{Path: main, Pos: text.Pos{Line: 3, Col: 1}})
	if err != nil || h == nil || h.Contents.Text == "" {
		t.Errorf("hover %+v %v", h, err)
	}
	list, err := c.Complete(ctx, lsp.Loc{Path: main, Pos: text.Pos{Line: 1, Col: 1}})
	if err != nil || list == nil || len(list.Items) == 0 {
		t.Errorf("completion %+v %v", list, err)
	}
	select {
	case s := <-diags:
		if s.Path != main || len(s.Diagnostics) == 0 {
			t.Errorf("diagnostics %+v", s)
		}
	case <-ctx.Done():
		t.Fatal("no diagnostics")
	}
	if err := m.Shutdown(ctx); err != nil {
		t.Error(err)
	}
}
