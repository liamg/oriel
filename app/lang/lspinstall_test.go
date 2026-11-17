package lang_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/registry"
	reglang "github.com/liamg/oriel/registry/lang"
)

// A file whose language has servers, none of them found, says so once, with an Install button when Oriel can
// install one here (npm is on this machine). Installing puts it in Oriel's own directory, and the file is opened on
// it as it's next drawn.
func TestAMissingServerIsOfferedAndInstalled(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("XDG_DATA_HOME", "") // Oriel's own directory is under the test's home
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(`#!/bin/sh
mkdir -p "$5/node_modules/.bin" && printf '#!/bin/sh\nexit 1\n' > "$5/node_modules/.bin/fake-ls" && chmod +x "$5/node_modules/.bin/fake-ls"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(file, []byte("const x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.Registry().Contribute(reglang.LSPServer{Name: "fake-ls", Language: []string{"typescript"}, Cmd: []string{"fake-ls", "--stdio"},
		Roots: []string{".git"}, Source: "test", Install: &lsp.Install{Tool: lsp.ToolNPM, Packages: []string{"fake-ls"}}})
	s.Run("editor.open", registry.Args{"path": file})
	s.Until("the missing server's notice", func() bool {
		return slices.ContainsFunc(s.Notices(), func(n string) bool { return strings.Contains(n, "fake-ls isn't installed") })
	})
	s.ClickText("Install fake-ls")
	s.Until("installed", func() bool {
		return slices.ContainsFunc(s.Notices(), func(n string) bool { return strings.Contains(n, "Installed fake-ls") })
	})
	own := apptest.PluginOf[*lang.LSP](s, "lsp").InstallDir
	if !strings.HasPrefix(own, os.Getenv("HOME")) {
		t.Errorf("Oriel's own directory is outside the test's home: %s", own)
	}
	if _, err := os.Stat(filepath.Join(own, "fake-ls", "node_modules", ".bin", "fake-ls")); err != nil {
		t.Errorf("not in Oriel's own directory: %v", err)
	}
	s.Until("the file opened on the installed server", func() bool {
		s.Frame()
		return slices.ContainsFunc(s.Logs.Ring.Records(), func(r logs.Record) bool { return r.Msg == "lsp open" && strings.Contains(r.Text(), file) })
	})
	n := slices.IndexFunc(s.Store().State().Notifications, func(n core.Notification) bool { return n.ID == "lsp.missing.typescript" })
	if n >= 0 && !s.Store().State().Notifications[n].Dismissed {
		t.Error("the missing server's notice stayed up after installing it")
	}
}
