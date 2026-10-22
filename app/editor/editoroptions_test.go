package editor_test

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
)

// :set sw=2 in one buffer changes only that buffer, shows on the settings page with :set as its source, and a
// settings-page change is seen by the next >>.
func TestSetIsPerBufferAndOnThePage(t *testing.T) {
	s := editorScene(t)
	dir := s.Editor().Dir()
	os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x\n"), 0o644)
	e := engine(s)

	keys(s, ":set sw=2 et<CR>gg>>")
	if got := s.Line(0); got != "  package pricing" {
		t.Fatalf("after :set sw=2 et and >>: %q", got)
	}
	if v, l, _ := s.Registry().Options().Get("shiftwidth", s.Host().Here()); v != 2 || l != registry.LayerBuffer {
		t.Errorf("shiftwidth is %v from %v", v, l)
	}
	s.Run("settings.open", registry.Args{"query": "shift_width"})
	s.Frame()
	s.Frame()
	if text := s.FullText(); !strings.Contains(text, "editor.shift_width") || !strings.Contains(text, "buffer · :set") {
		t.Errorf("the page doesn't show :set:\n%s", text)
	}
	s.Close("settings")

	e.Run("e other.txt")
	keys(s, ">>")
	if got := s.Line(0); got != "\tx" {
		t.Errorf("the other buffer took the first's shiftwidth: %q", got)
	}
	s.Run("settings.set", registry.Args{"name": "editor.shift_width", "value": 4, "scope": "user"})
	keys(s, "u>>")
	if got := s.Line(0); got != "    x" {
		t.Errorf("after the page set 4: %q", got)
	}
	e.Run("e #")
	keys(s, ">>")
	if got := s.Line(0); got != "    package pricing" {
		t.Errorf("the first buffer's own :set lost to the page's user value: %q", got)
	}
}

// With editor.clipboard = registers, yy leaves the system clipboard alone and "+yy sets it; with system, yy sets
// it.
func TestEditorClipboardOption(t *testing.T) {
	s := editorScene(t)
	s.Desktop.Text = ""
	keys(s, "ggyy")
	if s.Desktop.Text != "package pricing\n" {
		t.Errorf("system: yy put %q on the clipboard", s.Desktop.Text)
	}
	s.Desktop.Text = ""
	s.Run("settings.set", registry.Args{"name": "editor.clipboard", "value": "registers", "scope": "user"})
	keys(s, "yy")
	if s.Desktop.Text != "" {
		t.Errorf("registers: yy put %q on the clipboard", s.Desktop.Text)
	}
	keys(s, `"+yy`)
	if s.Desktop.Text != "package pricing\n" {
		t.Errorf(`registers: "+yy put %q on the clipboard`, s.Desktop.Text)
	}
	keys(s, ":set clipboard=unnamedplus<CR>")
	if v, _, _ := s.Registry().Options().Get("editor.clipboard", s.Host().Here()); v != "system" {
		t.Errorf("vim's unnamedplus is %v", v)
	}
}

// docs/configuration.md, "Option list": every registered option has a row, and every row names a registered option.
func TestOptionList(t *testing.T) {
	claude, codex := agenttest.NewFake(), agenttest.NewFake()
	claude.Name, codex.Name = "claude", "codex"
	// The built-in providers' plugins register options of their own.
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir(), Size: apptest.MockupSize, Config: apptest.Builtin(claude, codex)})
	f, err := os.Open(filepath.Join("..", "..", "docs", "configuration.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rows []string
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "## "):
			in = l == "## Option list"
		case in && strings.HasPrefix(l, "| `"):
			name, _, _ := strings.Cut(strings.TrimPrefix(l, "| `"), "`")
			rows = append(rows, name)
		}
	}
	var registered []string
	for _, sp := range s.Registry().Options().Specs() {
		registered = append(registered, sp.Name)
		if !slices.Contains(rows, sp.Name) {
			t.Errorf("%s is registered but has no row in configuration.md's option list", sp.Name)
		}
	}
	for _, r := range rows {
		if !slices.Contains(registered, r) {
			t.Errorf("configuration.md's option list has %s, which nothing registers", r)
		}
	}
}
