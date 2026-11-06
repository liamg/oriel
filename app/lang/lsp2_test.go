package lang_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/core"
	reglang "github.com/liamg/oriel/registry/lang"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/text"
)

// gd goes to the definition, in the file it's in.
func TestLSPDefinition(t *testing.T) {
	s, root := lspScene(t, "go", "gopls", "main.go")
	cursorAt(s, 8, 11)
	s.Press("gd")
	lspSettle(s)
	p := s.EditorWindow()
	if filepath.Join(root, "lib.go") != p.Path || p.Cursor.Line != 5 || p.Cursor.Col != 5 {
		t.Errorf("gd went to %s %+v, want lib.go 6:6", p.Path, p.Cursor)
	}
}

// motion.txt *jump-motions* ("the commands that start editing a new file"): gd is a vim jump in the
// window it's typed in, to another file or within one: ⌃O comes back, ⌃I goes there again.
func TestLSPDefinitionIsAJump(t *testing.T) {
	s, root := lspScene(t, "go", "gopls", "main.go")
	win := s.EditorWindow().ID
	at := func(what, file string, line, col int) {
		t.Helper()
		p := s.EditorWindow()
		if p.Path != filepath.Join(root, file) || p.Cursor.Line != line || col >= 0 && p.Cursor.Col != col {
			t.Errorf("%s: at %s %+v, want %s %d:%d", what, p.Path, p.Cursor, file, line+1, col+1)
		}
		if n := editorWindows(s); p.ID != win || n != 1 {
			t.Errorf("%s: in window %s of %d, want the window gd was typed in", what, p.ID, n)
		}
	}
	cursorAt(s, 8, 11)
	s.Press("gd")
	lspSettle(s)
	at("gd", "lib.go", 5, 5)
	s.Press("<C-o>")
	lspSettle(s)
	at("⌃O", "main.go", 8, 11)
	s.Press("<C-i>")
	lspSettle(s)
	at("⌃I", "lib.go", 5, 5)

	s.Press("grr")
	lspSettle(s)
	s.Press("j")
	s.Press("<CR>")
	lspSettle(s)
	at("⏎ on a reference in the same file", "lib.go", 11, -1)
	s.Press("<C-o>")
	lspSettle(s)
	at("⌃O in the same file", "lib.go", 5, 5)
}

// grr peeks at the references inline: a list by file beside the code of the one highlighted; j moves, ⏎ goes
// there, Esc closes it.
func TestLSPReferencesPeek(t *testing.T) {
	s, root := lspScene(t, "go", "gopls", "main.go")
	cursorAt(s, 8, 11)
	s.Press("grr")
	lspSettle(s)
	wantText(t, s, "the peek", "3 references", "lib.go", "main.go", "func greeting(name string) string {")
	if modes := s.KeyModes(); modes[0] != lang.PeekKeyMode {
		t.Fatalf("keys while peeking go to %v", modes)
	}
	s.Press("j")
	lspSettle(s)
	wantText(t, s, "the second reference", "2 / 3")
	s.Press("<CR>")
	lspSettle(s)
	p := s.EditorWindow()
	if p.Path != filepath.Join(root, "lib.go") || p.Cursor.Line != 11 {
		t.Errorf("⏎ went to %s %+v, want lib.go:12", p.Path, p.Cursor)
	}
	if slices.Contains(s.KeyModes(), lang.PeekKeyMode) {
		t.Error("the peek stayed open")
	}
}

// grn renames with the server, showing every file's diff before anything changes; Apply changes an
// open file's buffer and writes the others.
func TestLSPRenamePreview(t *testing.T) {
	s, root := lspScene(t, "go", "gopls", "main.go")
	cursorAt(s, 8, 11)
	s.Press("grn")
	lspSettle(s)
	in := s.Find("New name")
	if in == nil || !in.Focused || in.Value != "greeting" {
		t.Fatalf("the rename prompt: %+v", in)
	}
	s.TypeText("salute")
	s.UI().Key(render.KeyEvent{Key: "CR"})
	lspSettle(s)
	if !s.Showing(kit.ReplaceView) {
		t.Fatalf("no preview:\n%s", s.Text())
	}
	wantText(t, s, "the rename's diff", "Rename greeting → salute", "lib.go", "main.go", "- func greeting(name string) string {",
		"+ func salute(name string) string {", "+         out += salute(n)")
	if text := s.Text(); strings.Contains(text, root) || strings.Contains(text, "\t") {
		t.Errorf("the diff shows the work dir's path or a raw tab:\n%s", text)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "lib.go")); strings.Contains(string(b), "salute") {
		t.Fatal("the preview wrote lib.go")
	}
	s.ClickOn("Apply")
	lspSettle(s)
	if b, _ := os.ReadFile(filepath.Join(root, "lib.go")); !strings.Contains(string(b), "func salute(name string) string") {
		t.Errorf("lib.go after Apply:\n%s", b)
	}
	if got := s.Line(8); got != "\t\tout += salute(n)" {
		t.Errorf("main.go's buffer after Apply: %q", got)
	}
}

// The symbols pickers ask the server: this file's (gO, ␣s) and the workspace's (␣S).
func TestLSPSymbols(t *testing.T) {
	s, root := lspScene(t, "go", "gopls", "lib.go")
	src := pickerSource(t, s, "symbols.file")
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
	ws := pickerSource(t, s, "symbols")
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
		lspSettle(s)
	}
	if !found {
		t.Error("the workspace symbols lack greeting")
	}
	if k, _ := s.Registry().Lookup([]string{"n"}, []string{"g", "O"}); k == nil || k.Args["source"] != "symbols.file" {
		t.Errorf("gO is %+v", k)
	}
}

// The call hierarchy: who calls greeting, and what shout calls, in a picker.
func TestLSPCallHierarchy(t *testing.T) {
	s, _ := lspScene(t, "go", "gopls", "lib.go")
	cursorAt(s, 5, 6)
	s.Run("lsp.calls.incoming", nil)
	lspSettle(s)
	src := pickerSource(t, s, "lsp.calls")
	var labels []string
	for _, it := range src.Items(nil, "") {
		labels = append(labels, it.Label)
	}
	if got := strings.Join(labels, " "); !strings.Contains(got, "Welcome") {
		t.Errorf("incoming calls to greeting: %q", got)
	}
	wantText(t, s, "the picker", "CALLS", "Welcome")
}

// A config registers a language server from Lua; one with a built-in's name replaces it.
func TestLSPServerFromLua(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.Lua(`L.lsp.server{ name = "gopls", languages = { "go" }, cmd = { "my-gopls", "serve" }, roots = { "go.mod" }, settings = { gopls = { staticcheck = true } } }`)
	for _, sv := range reglang.LSPServers(s.Registry()) {
		if sv.Name == "gopls" {
			if sv.Cmd[0] != "my-gopls" || sv.Source != "test:1" || !strings.Contains(string(sv.Settings), `"staticcheck":true`) {
				t.Errorf("gopls is %+v", sv)
			}
			return
		}
	}
	t.Error("no gopls")
}

// gd typed while the file's server is still starting waits for it, saying so, rather than falling back to vim's gd.
func TestLSPDefinitionWaitsForTheServer(t *testing.T) {
	s, root := lspScene(t, "go", "gopls", "main.go")
	lspOf(s).ForgetDocs()
	s.Editor().SetCursor(text.Pos{Line: 8, Col: 11})
	s.Press("gd")
	ns := s.Store().State().Notifications
	if !slices.ContainsFunc(ns, func(n core.Notification) bool { return strings.Contains(n.Text, "Starting the go language server") }) {
		t.Errorf("no word that the server is starting: %+v", ns)
	}
	lspSettle(s)
	p := s.EditorWindow()
	if filepath.Join(root, "lib.go") != p.Path || p.Cursor.Line != 5 || p.Cursor.Col != 5 {
		t.Errorf("gd went to %s %+v, want lib.go 6:6", p.Path, p.Cursor)
	}
}

// References, rename, calls and code actions asked for while the file's server starts wait for it, saying so, as
// go to definition does.
func TestLSPRequestsWaitForTheServer(t *testing.T) {
	for _, keys := range []string{"grr", "grn", "gra"} {
		s, _ := lspScene(t, "go", "gopls", "main.go")
		lspOf(s).ForgetDocs()
		s.Editor().SetCursor(text.Pos{Line: 8, Col: 11})
		s.Press(keys)
		ns := s.Store().State().Notifications
		if !slices.ContainsFunc(ns, func(n core.Notification) bool { return strings.Contains(n.Text, "Starting the go language server") }) {
			t.Errorf("%s: no word that the server is starting: %+v", keys, ns)
		}
		s.Frame()
		s.Midway(func() { s.WantFrame("◌", "starting") })
		lspSettle(s)
	}
}

// editorWindows is how many editor windows the layout has.
func editorWindows(s *apptest.Scene) int {
	n := 0
	for _, v := range s.Host().Layout().All() {
		if v.ID == kit.EditorView {
			n++
		}
	}
	return n
}

// pickerSource is the picker source id, once every source has loaded.
func pickerSource(t *testing.T, s *apptest.Scene, id string) uireg.PickerSource {
	t.Helper()
	for _, src := range s.Registry().Pickers() {
		if src.Items != nil && src.Loading != nil {
			src.Items(nil, "")
		}
	}
	s.Until("the picker sources", func() bool {
		for _, src := range s.Registry().Pickers() {
			if src.Loading != nil && src.Loading() {
				return false
			}
		}
		return true
	})
	for _, src := range s.Registry().Pickers() {
		if src.ID == id {
			return src
		}
	}
	t.Fatalf("no source %s", id)
	return uireg.PickerSource{}
}
