package app

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
)

// line is the current buffer's line n (0-based).
func (s *errScene) line(n int) string { return string(s.editor().e.Buf().Line(n)) }

// ED-13: ":set sw=2 in one buffer changes only that buffer, shows on the settings page with :set as its source, and
// a settings-page change is seen by the next >>."
func TestSetIsPerBufferAndOnThePage(t *testing.T) {
	s := editorScene(t)
	dir := s.sh.cfg.WorkDir
	os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x\n"), 0o644)
	e := s.editor().e

	s.keys(":set sw=2 et<CR>gg>>")
	if got := s.line(0); got != "  package pricing" {
		t.Fatalf("after :set sw=2 et and >>: %q", got)
	}
	if v, l, _ := s.reg.Options().Get("shiftwidth", s.host.Here()); v != 2 || l != registry.LayerBuffer {
		t.Errorf("shiftwidth is %v from %v", v, l)
	}
	s.run("settings.open", registry.Args{"section": "editor"})
	s.frame()
	s.frame()
	if text := s.frameText(); !strings.Contains(text, "editor.shift_width") || !strings.Contains(text, "buffer · :set") {
		t.Errorf("the page doesn't show :set:\n%s", text)
	}
	s.settings().open = false

	e.Run("e other.txt")
	s.keys(">>")
	if got := s.line(0); got != "\tx" {
		t.Errorf("the other buffer took the first's shiftwidth: %q", got)
	}
	s.run("settings.set", registry.Args{"name": "editor.shift_width", "value": 4, "scope": "user"})
	s.keys("u>>")
	if got := s.line(0); got != "    x" {
		t.Errorf("after the page set 4: %q", got)
	}
	e.Run("e #")
	s.keys(">>")
	if got := s.line(0); got != "    package pricing" {
		t.Errorf("the first buffer's own :set lost to the page's user value: %q", got)
	}
}

// ED-13: "With editor.clipboard = registers, yy leaves the system clipboard alone and "+yy sets it; with system,
// yy sets it."
func TestEditorClipboardOption(t *testing.T) {
	s := editorScene(t)
	s.clip.text = ""
	s.keys("ggyy")
	if s.clip.text != "package pricing\n" {
		t.Errorf("system: yy put %q on the clipboard", s.clip.text)
	}
	s.clip.text = ""
	s.run("settings.set", registry.Args{"name": "editor.clipboard", "value": "registers", "scope": "user"})
	s.keys("yy")
	if s.clip.text != "" {
		t.Errorf("registers: yy put %q on the clipboard", s.clip.text)
	}
	s.keys(`"+yy`)
	if s.clip.text != "package pricing\n" {
		t.Errorf(`registers: "+yy put %q on the clipboard`, s.clip.text)
	}
	s.keys(":set clipboard=unnamedplus<CR>")
	if v, _, _ := s.reg.Options().Get("editor.clipboard", s.host.Here()); v != "system" {
		t.Errorf("vim's unnamedplus is %v", v)
	}
}

// settings.md, "Option list": every registered option has a row, and every row names a registered option.
func TestOptionList(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	f, err := os.Open(filepath.Join("..", "_design", "settings.md"))
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
	for _, sp := range s.reg.Options().Specs() {
		registered = append(registered, sp.Name)
		if !slices.Contains(rows, sp.Name) {
			t.Errorf("%s is registered but has no row in settings.md's option list", sp.Name)
		}
	}
	for _, r := range rows {
		if !slices.Contains(registered, r) {
			t.Errorf("settings.md's option list has %s, which nothing registers", r)
		}
	}
}
