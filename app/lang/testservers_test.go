package lang_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/registry"
)

// A test's app starts none of the machine's own language servers (gopls on PATH, say): only one the test registers.
// The app itself, with no such setting, starts every one installed.
func TestTestsStartNoBuiltInServers(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.Run("editor.open", registry.Args{"path": filepath.Join(dir, "main.go")})
	s.LSPSettle()
	if cs := apptest.PluginOf[*lang.LSP](s, "lsp").Clients(); len(cs) != 0 {
		t.Errorf("a test started %d language servers", len(cs))
	}
	if apptest.TestServers(lsp.ServerSpec{Name: "gopls", Source: "builtin:lsp"}) || !apptest.TestServers(lsp.ServerSpec{Name: "gopls", Source: "test"}) {
		t.Error("TestServers lets the wrong ones through")
	}
}
