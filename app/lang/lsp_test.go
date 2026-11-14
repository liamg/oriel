package lang_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
)

// The recorded gopls session replays to the UI: the seeded error is a diagnostic on its line and in
// Problems, and the server's state is in the status line.
func TestLSPDiagnostics(t *testing.T) {
	s, root := lspScene(t, "go", "gopls", "main.go")
	ds := s.WaitDiagnostics(filepath.Join(root, "main.go"))
	if ds[0].Line != 12 || ds[0].Owner != "gopls" || !strings.Contains(ds[0].Message, "greting") {
		t.Fatalf("diagnostics: %+v", ds)
	}
	if d := ds[0]; d.From != 25 || d.To != 32 {
		t.Errorf("the error spans bytes %d-%d, want greting's 25-32 (after a 4-byte emoji)", d.From, d.To)
	}
	wantText(t, s, "the editor and status line", "undefined: greting", "gopls")
}

// Completion as you type: a menu of the server's items for the word, its docs once resolved, <C-n> to move and
// <CR> to take one.
func TestLSPCompletion(t *testing.T) {
	s, _ := lspScene(t, "go", "gopls", "main.go")
	s.EditorKeys("11GS<Tab>gre") // as the recorded session typed it
	lspSettle(s)
	wantText(t, s, "the completion menu", "greeting", "func(name string) string")
	if !lspOf(s).CompletionOpen() {
		t.Fatal("the menu isn't open")
	}
	if modes := s.KeyModes(); modes[0] != lang.CompletionKeyMode {
		t.Fatalf("keys while the menu is open go to %v", modes)
	}
	s.Press("<CR>")
	lspSettle(s)
	if got := s.Line(10); !strings.HasPrefix(strings.TrimSpace(got), "greeting") {
		t.Errorf("after <CR> the line is %q", got)
	}
	if lspOf(s).CompletionOpen() {
		t.Error("the menu stayed open after taking an item")
	}
	if s.Editor().Mode() != "insert" {
		t.Errorf("taking an item left %s mode", s.Editor().Mode())
	}
}

// Signature help while typing a call's arguments.
func TestLSPSignatureHelp(t *testing.T) {
	s, _ := lspScene(t, "go", "gopls", "main.go")
	s.EditorKeys("11GSgreeting(")
	lspSettle(s)
	wantText(t, s, "signature help", "greeting(\nname string\n) string", "greeting says hello to name.")
	s.EditorKeys(")")
	lspSettle(s)
	if strings.Contains(s.Text(), "greeting says hello to name.") {
		t.Error("signature help stayed after the call closed")
	}
}

// K hovers: the symbol's signature and docs in a card at the cursor, gone when the cursor moves.
func TestLSPHover(t *testing.T) {
	s, _ := lspScene(t, "go", "gopls", "main.go")
	cursorAt(s, 8, 11)
	s.Press("K")
	lspSettle(s)
	wantText(t, s, "the hover card", "func greeting(name string) string", "greeting says hello to name.")
	s.EditorKeys("j")
	lspSettle(s)
	if strings.Contains(s.Text(), "greeting says hello to name.") {
		t.Error("the hover card stayed when the cursor moved")
	}
}

// Code actions for the diagnostic under the cursor, grouped, and taking one applies its edit.
func TestLSPCodeActions(t *testing.T) {
	s, root := lspScene(t, "go", "gopls", "main.go")
	cursorAt(s, 12, 26)
	s.Press("<Space>ca")
	lspSettle(s)
	wantText(t, s, "the code actions menu", "QUICK FIX", "Create function greting")
	s.Press("<CR>")
	lspSettle(s)
	text := strings.Join(fileLines(s, filepath.Join(root, "main.go")), "\n")
	if !strings.Contains(text, "func greting(") {
		t.Errorf("after taking the fix, main.go is:\n%s", text)
	}
}

// Inlay hints show inline, where the server puts them: a variable's type, a parameter's name.
func TestLSPInlayHints(t *testing.T) {
	s, _ := lspScene(t, "go", "gopls", "main.go")
	wantText(t, s, "inlay hints", "name:", "int")
	ds := s.Decorations(0, 15)
	found := false
	for _, d := range ds[8] {
		if d.Kind == uireg.Inlay && strings.TrimSpace(d.Text) == "name:" && d.From == 18 {
			found = true
		}
	}
	if !found {
		t.Errorf("no name: hint at line 9's argument: %+v", ds[8])
	}
}

// Recorded gopls/tsserver/pyright transcripts replay to the expected UI state: each server's seeded error
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
			ds := s.WaitDiagnostics(filepath.Join(root, c.file))
			if ds[0].Line != c.diagLine || ds[0].From != c.diagFrom || ds[0].Owner != c.server {
				t.Errorf("diagnostics %+v, want line %d from byte %d", ds, c.diagLine, c.diagFrom)
			}
			cursorAt(s, c.hoverLine, c.hoverCol)
			s.Press("K")
			lspSettle(s)
			wantText(t, s, "hover", c.hover)
			s.EditorKeys(fmt.Sprintf("%dGS%sgre", c.blank, c.indent))
			lspSettle(s)
			if !lspOf(s).CompletionOpen() {
				t.Fatal("no completion menu")
			}
			wantText(t, s, "completion", "greeting")
		})
	}
}

// :Inspect's language server section names the server and the semantic token under the cursor, as the server says.
func TestLSPInspect(t *testing.T) {
	s, root := lspScene(t, "go", "gopls", "main.go")
	cursorAt(s, 5, 6) // Welcome
	toks, err := lspOf(s).Clients()[0].SemanticTokens(context.Background(), filepath.Join(root, "main.go"))
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
	s.Run("Inspect", nil)
	lspSettle(s)
	text := strings.ReplaceAll(s.Text(), "\n", " ")
	for _, w := range []string{"LANGUAGE SERVER", "gopls", want} {
		if !strings.Contains(text, w) {
			t.Errorf(":Inspect lacks %q:\n%s", w, text)
		}
	}
}

// With lsp.start "project" (the default), a project's language servers start when it opens, before any of its files
// does, so the first gd doesn't wait for one; "file" waits for a file.
func TestLSPStartsWithTheProject(t *testing.T) {
	for _, mode := range []string{"project", "file"} {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/greet\n\ngo 1.22\n"), 0o644)
		s := apptest.New(t, apptest.Options{WorkDir: root})
		s.Registry().Options().Set("lsp.start", registry.LayerUser, registry.At{}, mode)
		resp := rig.Recorded(t, "go")
		s.Registry().Contribute(rig.ServerAnswering(t, "gopls", resp))
		lspSettle(s)
		started := slices.Contains(resp.Asked(), "initialize")
		if started != (mode == "project") {
			t.Errorf("lsp.start %q: started before a file opened = %v (asked %v)", mode, started, resp.Asked())
		}
	}
}

// Text stays inside the hover card and the references peek, however long the lines in them.
func TestLSPCardsKeepTheirText(t *testing.T) {
	s, _ := lspScene(t, "go", "gopls", "main.go")
	cursorAt(s, 8, 11)
	s.Press("K")
	lspSettle(s)
	lspOf(s).AddToHover("\n\n```go\nfunc greeting(name string, salutation string, punctuation string, locale string, formal bool) (string, error)\n```\n" +
		"A_paragraph_word_far_wider_than_the_card_that_has_nowhere_to_wrap_and_would_run_out_of_the_card_on_the_right")
	s.Frame()
	s.Frame()
	if o := s.UI().Overflowing(); len(o) > 0 {
		t.Errorf("the hover card's text runs out of its box:\n%s", strings.Join(o, "\n"))
	}
	s.Press("<Esc>")
	s.Resize(render.Point{X: 760, Y: s.Size().Y}) // the code beside the list has less room than its lines
	s.Press("grr")
	lspSettle(s)
	wantText(t, s, "the peek", "3 references")
	if o := s.UI().Overflowing(); len(o) > 0 {
		t.Errorf("the peek's text runs out of its box:\n%s", strings.Join(o, "\n"))
	}
}

// Typing doesn't ask the server for the file's inlay hints at every key: they're asked for again once the buffer
// has been still a moment.
func TestLSPInlayHintsWaitForTypingToStop(t *testing.T) {
	root := rig.LSPProject(t, "go")
	resp := rig.Recorded(t, "go")
	s := apptest.New(t, apptest.Options{WorkDir: root})
	s.Registry().Contribute(rig.ServerAnswering(t, "gopls", resp))
	s.Run("editor.open", registry.Args{"path": filepath.Join(root, "main.go")})
	s.LSPSettle()
	hints := func() int {
		n := 0
		for _, m := range resp.Asked() {
			if m == "textDocument/inlayHint" {
				n++
			}
		}
		return n
	}
	before := hints()
	if before == 0 {
		t.Fatal("no inlay hints asked for on opening the file")
	}
	s.EditorKeys("ggO")
	for _, k := range "typing" { // as quick as a person types: well inside the wait between keys
		s.EditorKeys(string(k))
		s.Frame()
		s.Advance(60 * time.Millisecond)
		s.RunQueued()
		s.Frame()
	}
	if n := hints() - before; n > 0 {
		t.Errorf("typing 6 letters asked for inlay hints %d times", n)
	}
}
