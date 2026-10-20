package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/lsp/lsptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/vim"
)

// lspScene is the lsp module's Go fixture open in the editor, its server answering from the recorded gopls session.
func lspScene(t *testing.T, fixture, server, file string) (*errScene, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("..", "lsp", "testdata", fixture)
	ents, _ := os.ReadDir(src)
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join(src, e.Name()))
		os.WriteFile(filepath.Join(root, strings.TrimSuffix(e.Name(), ".in")), b, 0o644)
	}
	tr, err := lsptest.Load(filepath.Join("..", "lsp", "testdata", "transcripts", fixture+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s := newScene(t, "", root, mockupSize)
	for _, spec := range lsp.Builtins() {
		if spec.Name == server {
			spec.Connect = lsptest.NewResponder(tr).Launcher()
			spec.Source = "test"
			s.reg.Contribute(registry.LSPServer(spec))
		}
	}
	s.run("editor.open", registry.Args{"path": filepath.Join(root, file)})
	s.lspSettle()
	return s, root
}

func (s *errScene) lsp() *lspPlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*lspPlugin); ok {
			return x
		}
	}
	return nil
}

// lspSettle frames until the language server's answers are in.
func (s *errScene) lspSettle() {
	for range 4 {
		s.frame()
		s.lsp().wait()
		s.sh.runTodo()
	}
	s.frame()
	s.frame()
}

func (s *errScene) cursorAt(line, col int) {
	s.editor().e.SetCursor(vim.Pos{Line: line, Col: col})
	s.lspSettle()
}

func (s *errScene) wantText(t *testing.T, what string, wants ...string) {
	t.Helper()
	text := s.frameText()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("%s: no %q in\n%s", what, w, text)
		}
	}
}

// LS-1: the recorded gopls session replays to the UI: the seeded error is a diagnostic on its line and in
// Problems, and the server's state is in the status line.
func TestLSPDiagnostics(t *testing.T) {
	s, root := lspScene(t, "go", "gopls", "main.go")
	ds := s.reg.DiagnosticsFor(filepath.Join(root, "main.go"))
	if len(ds) == 0 || ds[0].Line != 12 || ds[0].Owner != "gopls" || !strings.Contains(ds[0].Message, "greting") {
		t.Fatalf("diagnostics: %+v", ds)
	}
	if d := ds[0]; d.From != 25 || d.To != 32 {
		t.Errorf("the error spans bytes %d-%d, want greting's 25-32 (after a 4-byte emoji)", d.From, d.To)
	}
	s.wantText(t, "the editor and status line", "undefined: greting", "gopls")
}

// LS-1: completion as you type: a menu of the server's items for the word, its docs once resolved, <C-n> to move and
// <CR> to take one.
func TestLSPCompletion(t *testing.T) {
	s, _ := lspScene(t, "go", "gopls", "main.go")
	s.keys("11GS<Tab>gre") // as the recorded session typed it
	s.lspSettle()
	s.wantText(t, "the completion menu", "greeting", "func(name string) string")
	if !s.lsp().comp.open {
		t.Fatal("the menu isn't open")
	}
	if modes := s.sh.keyModes(); modes[0] != CompletionKeyMode {
		t.Fatalf("keys while the menu is open go to %v", modes)
	}
	s.keyCommand(t, "<CR>")
	s.lspSettle()
	if got := s.line(10); !strings.HasPrefix(strings.TrimSpace(got), "greeting") {
		t.Errorf("after <CR> the line is %q", got)
	}
	if s.lsp().comp.open {
		t.Error("the menu stayed open after taking an item")
	}
	if s.editor().e.Mode() != "insert" {
		t.Errorf("taking an item left %s mode", s.editor().e.Mode())
	}
}

// LS-1: signature help while typing a call's arguments.
func TestLSPSignatureHelp(t *testing.T) {
	s, _ := lspScene(t, "go", "gopls", "main.go")
	s.keys("11GSgreeting(")
	s.lspSettle()
	s.wantText(t, "signature help", "greeting(\nname string\n) string", "greeting says hello to name.")
	s.keys(")")
	s.lspSettle()
	if strings.Contains(s.frameText(), "greeting says hello to name.") {
		t.Error("signature help stayed after the call closed")
	}
}

// LS-1: K hovers: the symbol's signature and docs in a card at the cursor, gone when the cursor moves.
func TestLSPHover(t *testing.T) {
	s, _ := lspScene(t, "go", "gopls", "main.go")
	s.cursorAt(8, 11)
	s.keyCommand(t, "K")
	s.lspSettle()
	s.wantText(t, "the hover card", "func greeting(name string) string", "greeting says hello to name.")
	s.keys("j")
	s.lspSettle()
	if strings.Contains(s.frameText(), "greeting says hello to name.") {
		t.Error("the hover card stayed when the cursor moved")
	}
}

// LS-1: code actions for the diagnostic under the cursor, grouped, and taking one applies its edit.
func TestLSPCodeActions(t *testing.T) {
	s, root := lspScene(t, "go", "gopls", "main.go")
	s.cursorAt(12, 26)
	s.keyCommand(t, "<Space>ca")
	s.lspSettle()
	s.wantText(t, "the code actions menu", "QUICK FIX", "Create function greting")
	s.keyCommand(t, "<CR>")
	s.lspSettle()
	time.Sleep(10 * time.Millisecond)
	s.lspSettle()
	text := strings.Join(s.lineTexts(root, "main.go"), "\n")
	if !strings.Contains(text, "func greting(") {
		t.Errorf("after taking the fix, main.go is:\n%s", text)
	}
}

// lineTexts are a file's lines: its buffer's if it's open, else from disk.
func (s *errScene) lineTexts(root, name string) []string {
	if f, ok := s.editor().openBuffer(filepath.Join(root, name)); ok {
		return strings.Split(string(f.Bytes()), "\n")
	}
	b, _ := os.ReadFile(filepath.Join(root, name))
	return strings.Split(string(b), "\n")
}

// LS-1: inlay hints show inline, where the server puts them: a variable's type, a parameter's name.
func TestLSPInlayHints(t *testing.T) {
	s, _ := lspScene(t, "go", "gopls", "main.go")
	s.wantText(t, "inlay hints", "name:", "int")
	ds := s.editor().decorations(s.editor().current(), 0, 15, nil)
	found := false
	for _, d := range ds[8] {
		if d.Kind == registry.Inlay && strings.TrimSpace(d.Text) == "name:" && d.From == 18 {
			found = true
		}
	}
	if !found {
		t.Errorf("no name: hint at line 9's argument: %+v", ds[8])
	}
}

// LS-1: "recorded gopls/tsserver/pyright transcripts replay to the expected UI state": each server's seeded error
// is a diagnostic in bytes, K shows its hover, and typing on the blank line opens its completions.
func TestLSPTranscriptsReplayToTheUI(t *testing.T) {
	for _, c := range []struct {
		fixture, server, file string
		diagLine, diagFrom    int
		hoverLine, hoverCol   int
		hover                 string
		blank                 int    // the blank line the session typed on, 1-based
		indent                string // as the session typed it
	}{
		{"go", "gopls", "main.go", 12, 25, 8, 11, "func greeting(name string) string", 11, "<Tab>"},
		{"ts", "typescript-language-server", "main.ts", 10, 31, 6, 12, "(alias) greeting(name: string): string", 9, "  "},
		{"py", "pyright", "main.py", 10, 27, 7, 16, "(function) def greeting(name: str) -> str", 10, "    "},
	} {
		t.Run(c.server, func(t *testing.T) {
			s, root := lspScene(t, c.fixture, c.server, c.file)
			ds := s.reg.DiagnosticsFor(filepath.Join(root, c.file))
			if len(ds) == 0 || ds[0].Line != c.diagLine || ds[0].From != c.diagFrom || ds[0].Owner != c.server {
				t.Errorf("diagnostics %+v, want line %d from byte %d", ds, c.diagLine, c.diagFrom)
			}
			s.cursorAt(c.hoverLine, c.hoverCol)
			s.keyCommand(t, "K")
			s.lspSettle()
			s.wantText(t, "hover", c.hover)
			s.keys(fmt.Sprintf("%dGS%sgre", c.blank, c.indent))
			s.lspSettle()
			if !s.lsp().comp.open {
				t.Fatal("no completion menu")
			}
			s.wantText(t, "completion", "greeting")
		})
	}
}

// :Inspect's language server section names the server and the semantic token under the cursor, as the server says.
func TestLSPInspect(t *testing.T) {
	s, root := lspScene(t, "go", "gopls", "main.go")
	s.cursorAt(5, 6) // Welcome
	toks, err := s.lsp().m.Clients()[0].SemanticTokens(context.Background(), filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := ""
	for _, tk := range toks {
		if tk.Range.Start.Line == 5 && tk.Range.Start.Col <= 6 && 6 < tk.Range.End.Col {
			want = strings.TrimSpace(tk.Type + " " + strings.Join(tk.Modifiers, " "))
		}
	}
	if want == "" {
		t.Fatal("the recording has no token on Welcome")
	}
	s.run("Inspect", nil)
	s.lspSettle()
	text := strings.ReplaceAll(s.frameText(), "\n", " ")
	for _, w := range []string{"LANGUAGE SERVER", "gopls", want} {
		if !strings.Contains(text, w) {
			t.Errorf(":Inspect lacks %q:\n%s", w, text)
		}
	}
	if strings.Contains(text, "LS-1") {
		t.Error(":Inspect still says the language servers are to come")
	}
}

// With lsp.start "project" (the default), a project's language servers start when it opens, before any of its files
// does, so the first gd doesn't wait for one; "file" waits for a file.
func TestLSPStartsWithTheProject(t *testing.T) {
	for _, mode := range []string{"project", "file"} {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/greet\n\ngo 1.22\n"), 0o644)
		tr, err := lsptest.Load(filepath.Join("..", "lsp", "testdata", "transcripts", "go.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		s := newScene(t, "", root, mockupSize)
		s.reg.Options().Set("lsp.start", registry.LayerUser, registry.At{}, mode)
		resp := lsptest.NewResponder(tr)
		for _, spec := range lsp.Builtins() {
			if spec.Name == "gopls" {
				spec.Connect = resp.Launcher()
				spec.Source = "test"
				s.reg.Contribute(registry.LSPServer(spec))
			}
		}
		s.lspSettle()
		started := slices.Contains(resp.Asked(), "initialize")
		if started != (mode == "project") {
			t.Errorf("lsp.start %q: started before a file opened = %v (asked %v)", mode, started, resp.Asked())
		}
	}
}

// Text stays inside the hover card and the references peek, however long the lines in them.
func TestLSPCardsKeepTheirText(t *testing.T) {
	s, _ := lspScene(t, "go", "gopls", "main.go")
	s.cursorAt(8, 11)
	s.keyCommand(t, "K")
	s.lspSettle()
	s.lsp().hover.md.Text += "\n\n```go\nfunc greeting(name string, salutation string, punctuation string, locale string, formal bool) (string, error)\n```\n" +
		"A_paragraph_word_far_wider_than_the_card_that_has_nowhere_to_wrap_and_would_run_out_of_the_card_on_the_right"
	s.frame()
	s.frame()
	if o := s.ui.Overflowing(); len(o) > 0 {
		t.Errorf("the hover card's text runs out of its box:\n%s", strings.Join(o, "\n"))
	}
	s.keyCommand(t, "<Esc>")
	s.size.X = 760 // the code beside the list has less room than its lines
	s.keyCommand(t, "grr")
	s.lspSettle()
	s.wantText(t, "the peek", "3 references")
	if o := s.ui.Overflowing(); len(o) > 0 {
		t.Errorf("the peek's text runs out of its box:\n%s", strings.Join(o, "\n"))
	}
}
