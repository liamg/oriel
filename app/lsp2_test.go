package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// LS-2: gd goes to the definition, in the file it's in.
func TestLSPDefinition(t *testing.T) {
	s, root := lspScene(t, "go", "gopls", "main.go")
	s.cursorAt(8, 11)
	s.keyCommand(t, "gd")
	s.lspSettle()
	p := s.editor().current()
	if filepath.Join(root, "lib.go") != filepath.Join(root, p.Name()) || p.Cursor().Line != 5 || p.Cursor().Col != 5 {
		t.Errorf("gd went to %s %+v, want lib.go 6:6", p.Name(), p.Cursor())
	}
}

// LS-2: grr peeks at the references inline: a list by file beside the code of the one highlighted; j moves, ⏎ goes
// there, Esc closes it.
func TestLSPReferencesPeek(t *testing.T) {
	s, root := lspScene(t, "go", "gopls", "main.go")
	s.cursorAt(8, 11)
	s.keyCommand(t, "grr")
	s.lspSettle()
	s.wantText(t, "the peek", "3 references", "lib.go", "main.go", "func greeting(name string) string {")
	if modes := s.sh.keyModes(); modes[0] != PeekKeyMode {
		t.Fatalf("keys while peeking go to %v", modes)
	}
	s.keyCommand(t, "j")
	s.lspSettle()
	s.wantText(t, "the second reference", "2 / 3")
	s.keyCommand(t, "<CR>")
	s.lspSettle()
	p := s.editor().current()
	if filepath.Join(root, p.Name()) != filepath.Join(root, "lib.go") || p.Cursor().Line != 11 {
		t.Errorf("⏎ went to %s %+v, want lib.go:12", p.Name(), p.Cursor())
	}
	if s.lsp().peek != nil {
		t.Error("the peek stayed open")
	}
}

// LS-2 and ED-8: grn renames with the server, showing every file's diff before anything changes; Apply changes an
// open file's buffer and writes the others.
func TestLSPRenamePreview(t *testing.T) {
	s, root := lspScene(t, "go", "gopls", "main.go")
	s.cursorAt(8, 11)
	s.keyCommand(t, "grn")
	s.lspSettle()
	in := s.access("New name")
	if in == nil || !in.Focused || in.Value != "greeting" {
		t.Fatalf("the rename prompt: %+v", in)
	}
	for _, r := range "salute" {
		s.ui.Key(render.KeyEvent{Key: string(r)})
		s.frame()
	}
	s.ui.Key(render.KeyEvent{Key: "CR"})
	s.lspSettle()
	if !s.showing(ReplaceView) {
		t.Fatalf("no preview:\n%s", s.frameText())
	}
	s.wantText(t, "the rename's diff", "Rename greeting → salute", "lib.go", "main.go", "- func greeting(name string) string {",
		"+ func salute(name string) string {", "+ \t\tout += salute(n)")
	if b, _ := os.ReadFile(filepath.Join(root, "lib.go")); strings.Contains(string(b), "salute") {
		t.Fatal("the preview wrote lib.go")
	}
	s.click(centre(s.access("Apply").Bounds))
	s.lspSettle()
	if b, _ := os.ReadFile(filepath.Join(root, "lib.go")); !strings.Contains(string(b), "func salute(name string) string") {
		t.Errorf("lib.go after Apply:\n%s", b)
	}
	if got := s.line(8); got != "\t\tout += salute(n)" {
		t.Errorf("main.go's buffer after Apply: %q", got)
	}
}

// LS-2: the symbols pickers ask the server: this file's (gO, ␣s) and the workspace's (␣S).
func TestLSPSymbols(t *testing.T) {
	s, root := lspScene(t, "go", "gopls", "lib.go")
	src := mustSource(t, s, "symbols.file")
	items := src.Items(nil, "")
	var labels []string
	for _, it := range items {
		labels = append(labels, it.Label)
	}
	if got := strings.Join(labels, " "); !strings.Contains(got, "greeting") || !strings.Contains(got, "shout") {
		t.Errorf("this file's symbols: %q", got)
	}
	for _, it := range items {
		if it.Label == "greeting" && (filepath.Join(root, it.Path) != filepath.Join(root, "lib.go") || it.Line != 6) {
			t.Errorf("greeting is at %s:%d", it.Path, it.Line)
		}
	}
	ws := mustSource(t, s, "symbols")
	found := false
	for range 50 {
		for _, it := range ws.Items(nil, "greeting") {
			if it.Label == "greeting" {
				found = true
			}
		}
		if found {
			break
		}
		s.lspSettle()
	}
	if !found {
		t.Error("the workspace symbols lack greeting")
	}
	if k, _ := s.reg.Lookup([]string{"n"}, []string{"g", "O"}); k == nil || k.Args["source"] != "symbols.file" {
		t.Errorf("gO is %+v", k)
	}
}

// LS-2: the call hierarchy: who calls greeting, and what shout calls, in a picker.
func TestLSPCallHierarchy(t *testing.T) {
	s, _ := lspScene(t, "go", "gopls", "lib.go")
	s.cursorAt(5, 6)
	s.run("lsp.calls.incoming", nil)
	s.lspSettle()
	src := mustSource(t, s, "lsp.calls")
	var labels []string
	for _, it := range src.Items(nil, "") {
		labels = append(labels, it.Label)
	}
	if got := strings.Join(labels, " "); !strings.Contains(got, "Welcome") {
		t.Errorf("incoming calls to greeting: %q", got)
	}
	s.wantText(t, "the picker", "CALLS", "Welcome")
}

// LS-1: a config registers a language server from Lua; one with a built-in's name replaces it.
func TestLSPServerFromLua(t *testing.T) {
	s := newErrScene(t, "")
	s.eval(t, `L.lsp.server{ name = "gopls", languages = { "go" }, cmd = { "my-gopls", "serve" }, roots = { "go.mod" }, settings = { gopls = { staticcheck = true } } }`)
	for _, sv := range registry.LSPServers(s.reg) {
		if sv.Name == "gopls" {
			if sv.Cmd[0] != "my-gopls" || sv.Source != "test:1" || !strings.Contains(string(sv.Settings), `"staticcheck":true`) {
				t.Errorf("gopls is %+v", sv)
			}
			return
		}
	}
	t.Error("no gopls")
}
