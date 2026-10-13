package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// conventionsScene is the built-ins over a work tree with an .editorconfig, a nested one and a .gitattributes.
func conventionsScene(t *testing.T) (*errScene, string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		".editorconfig": `root = true

[*]
indent_style = space
indent_size = 4
end_of_line = lf
trim_trailing_whitespace = true

[*.go]
indent_style = tab
tab_width = 3

[nofinal.txt]
insert_final_newline = false
`,
		"docs/.editorconfig": "[*.md]\nindent_size = 2\n",
		".gitattributes":     "*.bat eol=crlf\n*.dat binary\n*.csv eol=crlf\ngen/** linguist-generated\n",
		"main.go":            "package main\n",
		"docs/guide.md":      "# guide\n",
		"run.bat":            "echo hi\n",
		"table.csv":          "a,b\n",
		"data.dat":           "raw\n",
		"gen/api.go":         "package gen\n",
		"nofinal.txt":        "no newline\n",
		"ml.txt":             "text\n// vim: set ts=3 sw=6 et:\n",
	}
	for name, src := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	s := newScene(t, "", dir, render.Point{X: 1000, Y: 700})
	return s, dir
}

func (s *errScene) open(t *testing.T, name string) registry.At {
	t.Helper()
	s.run("editor.open", registry.Args{"path": name})
	s.editor().conv.refresh()
	return s.host.Here()
}

func (s *errScene) option(t *testing.T, name string, at registry.At) (any, string) {
	t.Helper()
	v, _, err := s.reg.Options().Get(name, at)
	if err != nil {
		t.Fatal(err)
	}
	return v, s.reg.Options().Source(name, at)
}

// ED-9: "a buffer opened under a fixture tree gets each property from the right section, visible with its source on
// the settings page; editing .editorconfig re-applies live".
func TestEditorConfigApplies(t *testing.T) {
	s, dir := conventionsScene(t)
	at := s.open(t, "main.go")
	for _, tt := range []struct {
		option string
		want   any
		source string
	}{
		// ref:   editorconfig/index.rst#File Processing
		// quote: "the pairs defined in the section that comes later in the .editorconfig file take precedence"
		{"editor.expand_tab", false, ".editorconfig:10"},
		{"editor.tab_stop", 3, ".editorconfig:11"},
		{"editor.shift_width", 4, ".editorconfig:5"},
		{"editor.end_of_line", "lf", ".editorconfig:6"},
		{"editor.trim_trailing_whitespace", true, ".editorconfig:7"},
	} {
		if v, src := s.option(t, tt.option, at); v != tt.want || src != tt.source {
			t.Errorf("main.go %s = %v from %q, want %v from %q", tt.option, v, src, tt.want, tt.source)
		}
	}
	// ref:   editorconfig/index.rst#File Processing
	// quote: "pairs in closer files take precedence"
	at = s.open(t, "docs/guide.md")
	if v, src := s.option(t, "editor.shift_width", at); v != 2 || src != "docs/.editorconfig:2" {
		t.Errorf("guide.md shift_width = %v from %q", v, src)
	}
	if v, _ := s.option(t, "editor.expand_tab", at); v != true {
		t.Errorf("guide.md expand_tab = %v", v)
	}

	// On the settings page, with where it came from.
	at = s.open(t, "main.go")
	s.run("settings.open", registry.Args{"query": "tab_stop"})
	s.frame()
	s.frame()
	if text := s.frameText(); !strings.Contains(text, "file · .editorconfig:11") {
		t.Errorf("the page doesn't show the file's source:\n%s", text)
	}
	s.settings().open = false

	// Live: edit .editorconfig, and the next look applies it.
	src, _ := os.ReadFile(filepath.Join(dir, ".editorconfig"))
	os.WriteFile(filepath.Join(dir, ".editorconfig"), []byte(strings.Replace(string(src), "tab_width = 3", "tab_width = 5", 1)), 0o644)
	s.editor().conv.refresh()
	if v, _ := s.option(t, "editor.tab_stop", at); v != 5 {
		t.Errorf("after the edit tab_stop = %v", v)
	}

	// :set still wins over the file's conventions.
	s.keys(":set ts=7<CR>")
	if v, src := s.option(t, "editor.tab_stop", at); v != 7 || src != ":set" {
		t.Errorf("after :set ts=7: %v from %q", v, src)
	}
}

// .gitattributes: eol under .editorconfig's end_of_line, binary opens read-only, generated is marked.
// ref:   oracle git check-attr: gitx/attr matches it (TestMatchesGitCheckAttr); gitattributes(5), "eol",
// https://git-scm.com/docs/gitattributes#_eol
func TestGitAttributesApply(t *testing.T) {
	s, dir := conventionsScene(t)
	at := s.open(t, "run.bat")
	if v, src := s.option(t, "editor.end_of_line", at); v != "lf" || src != ".editorconfig:6" {
		t.Errorf("run.bat end_of_line = %v from %q; .editorconfig's comes first", v, src)
	}
	os.WriteFile(filepath.Join(dir, ".editorconfig"), []byte("root = true\n"), 0o644)
	s.editor().conv.refresh()
	if v, src := s.option(t, "editor.end_of_line", at); v != "crlf" || src != ".gitattributes:1" {
		t.Errorf("run.bat end_of_line = %v from %q without .editorconfig", v, src)
	}
	s.keys("ggAx<Esc>:w<CR>")
	if got, _ := os.ReadFile(filepath.Join(dir, "run.bat")); string(got) != "echo hix\r\n" {
		t.Errorf("run.bat written as %q", got)
	}

	s.open(t, "data.dat")
	if ro := s.editor().e.ReadOnly(); !strings.Contains(ro, "binary") {
		t.Errorf("data.dat read-only: %q", ro)
	}
	at = s.open(t, "gen/api.go")
	if v, src := s.option(t, "files.generated", at); v != true || src != ".gitattributes:4" {
		t.Errorf("gen/api.go generated = %v from %q", v, src)
	}
}

// Saving applies trim_trailing_whitespace and insert_final_newline.
// ref:   editorconfig/index.rst#Supported Pairs
// quote: "Set to true to remove all whitespace characters preceding newline characters in the file"
func TestConventionsOnSave(t *testing.T) {
	s, dir := conventionsScene(t)
	s.open(t, "main.go")
	s.keys("ggA   <Esc>:w<CR>")
	if got, _ := os.ReadFile(filepath.Join(dir, "main.go")); string(got) != "package main\n" {
		t.Errorf("main.go written as %q", got)
	}
	if got := s.line(0); got != "package main" {
		t.Errorf("the buffer kept its trailing white space: %q", got)
	}
	// ref:   editorconfig/index.rst#Supported Pairs
	// quote: "and false to ensure it doesn't"
	s.open(t, "nofinal.txt")
	s.keys(":w<CR>")
	if got, _ := os.ReadFile(filepath.Join(dir, "nofinal.txt")); string(got) != "no newline" {
		t.Errorf("nofinal.txt written as %q", got)
	}
}

// Modelines are off unless editor.modelines is on, and then only display and indent options take.
// ref:   nvim/options.txt *modeline*
// quote: "[text{white}]{vi:|vim:|Vim:|ex:}[white]se[t] {options}:[text]"
func TestModelines(t *testing.T) {
	s, _ := conventionsScene(t)
	at := s.open(t, "ml.txt")
	if v, src := s.option(t, "editor.tab_stop", at); v != 4 || src != ".editorconfig:5" {
		t.Errorf("a modeline applied while modelines are off: ts = %v from %q", v, src)
	}
	s.run("settings.set", registry.Args{"name": "editor.modelines", "value": true, "scope": "user"})
	s.editor().conv.refresh()
	for _, tt := range []struct {
		option string
		want   any
	}{{"editor.tab_stop", 3}, {"editor.shift_width", 6}, {"editor.expand_tab", true}} {
		if v, src := s.option(t, tt.option, at); v != tt.want || src != "ml.txt:2 (modeline)" {
			t.Errorf("%s = %v from %q", tt.option, v, src)
		}
	}
}

// Project search leaves out generated files unless asked.
func TestSearchSkipsGenerated(t *testing.T) {
	s, _ := conventionsScene(t)
	got := s.editor().conv.searchable([]string{"main.go", "gen/api.go", "docs/guide.md"})
	if strings.Join(got, ",") != "main.go,docs/guide.md" {
		t.Errorf("searchable: %v", got)
	}
}
