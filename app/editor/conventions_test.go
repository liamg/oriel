package editor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// conventionsScene is the built-ins over a work tree with an .editorconfig, a nested one and a .gitattributes.
func conventionsScene(t *testing.T) (*apptest.Scene, string) {
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
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: render.Point{X: 1000, Y: 700}})
	return s, dir
}

// A buffer opened under a fixture tree gets each property from the right section, visible with its source on the
// settings page; editing .editorconfig re-applies live.
func TestEditorConfigApplies(t *testing.T) {
	s, dir := conventionsScene(t)
	at := open(s, t, "main.go")
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
		if v, src := option(s, t, tt.option, at); v != tt.want || src != tt.source {
			t.Errorf("main.go %s = %v from %q, want %v from %q", tt.option, v, src, tt.want, tt.source)
		}
	}
	// ref:   editorconfig/index.rst#File Processing
	// quote: "pairs in closer files take precedence"
	at = open(s, t, "docs/guide.md")
	if v, src := option(s, t, "editor.shift_width", at); v != 2 || src != "docs/.editorconfig:2" {
		t.Errorf("guide.md shift_width = %v from %q", v, src)
	}
	if v, _ := option(s, t, "editor.expand_tab", at); v != true {
		t.Errorf("guide.md expand_tab = %v", v)
	}

	// On the settings page, with where it came from.
	at = open(s, t, "main.go")
	s.Run("settings.open", registry.Args{"query": "tab_stop"})
	s.Frame()
	s.Frame()
	if text := s.Text(); !strings.Contains(text, "file · .editorconfig:11") {
		t.Errorf("the page doesn't show the file's source:\n%s", text)
	}
	s.Close("settings")

	// Live: edit .editorconfig, and the next look applies it.
	src, _ := os.ReadFile(filepath.Join(dir, ".editorconfig"))
	os.WriteFile(filepath.Join(dir, ".editorconfig"), []byte(strings.Replace(string(src), "tab_width = 3", "tab_width = 5", 1)), 0o644)
	editorOf(s).RefreshConventions()
	if v, _ := option(s, t, "editor.tab_stop", at); v != 5 {
		t.Errorf("after the edit tab_stop = %v", v)
	}

	// :set still wins over the file's conventions.
	keys(s, ":set ts=7<CR>")
	if v, src := option(s, t, "editor.tab_stop", at); v != 7 || src != ":set" {
		t.Errorf("after :set ts=7: %v from %q", v, src)
	}
}

// .gitattributes: eol under .editorconfig's end_of_line, binary opens read-only, generated is marked.
// ref:   oracle git check-attr: gitx/attr matches it (TestMatchesGitCheckAttr); gitattributes(5), "eol",
// https://git-scm.com/docs/gitattributes#_eol
func TestGitAttributesApply(t *testing.T) {
	s, dir := conventionsScene(t)
	at := open(s, t, "run.bat")
	if v, src := option(s, t, "editor.end_of_line", at); v != "lf" || src != ".editorconfig:6" {
		t.Errorf("run.bat end_of_line = %v from %q; .editorconfig's comes first", v, src)
	}
	os.WriteFile(filepath.Join(dir, ".editorconfig"), []byte("root = true\n"), 0o644)
	editorOf(s).RefreshConventions()
	if v, src := option(s, t, "editor.end_of_line", at); v != "crlf" || src != ".gitattributes:1" {
		t.Errorf("run.bat end_of_line = %v from %q without .editorconfig", v, src)
	}
	keys(s, "ggAx<Esc>:w<CR>")
	if got, _ := os.ReadFile(filepath.Join(dir, "run.bat")); string(got) != "echo hix\r\n" {
		t.Errorf("run.bat written as %q", got)
	}

	open(s, t, "data.dat")
	if ro := engine(s).ReadOnly(); !strings.Contains(ro, "binary") {
		t.Errorf("data.dat read-only: %q", ro)
	}
	at = open(s, t, "gen/api.go")
	if v, src := option(s, t, "files.generated", at); v != true || src != ".gitattributes:4" {
		t.Errorf("gen/api.go generated = %v from %q", v, src)
	}
}

// Saving applies trim_trailing_whitespace and insert_final_newline.
// ref:   editorconfig/index.rst#Supported Pairs
// quote: "Set to true to remove all whitespace characters preceding newline characters in the file"
func TestConventionsOnSave(t *testing.T) {
	s, dir := conventionsScene(t)
	open(s, t, "main.go")
	keys(s, "ggA   <Esc>:w<CR>")
	if got, _ := os.ReadFile(filepath.Join(dir, "main.go")); string(got) != "package main\n" {
		t.Errorf("main.go written as %q", got)
	}
	if got := s.Line(0); got != "package main" {
		t.Errorf("the buffer kept its trailing white space: %q", got)
	}
	// ref:   editorconfig/index.rst#Supported Pairs
	// quote: "and false to ensure it doesn't"
	open(s, t, "nofinal.txt")
	keys(s, ":w<CR>")
	if got, _ := os.ReadFile(filepath.Join(dir, "nofinal.txt")); string(got) != "no newline" {
		t.Errorf("nofinal.txt written as %q", got)
	}
}

// Modelines are off unless editor.modelines is on, and then only display and indent options take.
// ref:   nvim/options.txt *modeline*
// quote: "[text{white}]{vi:|vim:|Vim:|ex:}[white]se[t] {options}:[text]"
func TestModelines(t *testing.T) {
	s, _ := conventionsScene(t)
	at := open(s, t, "ml.txt")
	if v, src := option(s, t, "editor.tab_stop", at); v != 4 || src != ".editorconfig:5" {
		t.Errorf("a modeline applied while modelines are off: ts = %v from %q", v, src)
	}
	s.Run("settings.set", registry.Args{"name": "editor.modelines", "value": true, "scope": "user"})
	editorOf(s).RefreshConventions()
	for _, tt := range []struct {
		option string
		want   any
	}{{"editor.tab_stop", 3}, {"editor.shift_width", 6}, {"editor.expand_tab", true}} {
		if v, src := option(s, t, tt.option, at); v != tt.want || src != "ml.txt:2 (modeline)" {
			t.Errorf("%s = %v from %q", tt.option, v, src)
		}
	}
}

// Project search leaves out generated files unless asked.
func TestSearchSkipsGenerated(t *testing.T) {
	s, _ := conventionsScene(t)
	got := editorOf(s).Searchable([]string{"main.go", "gen/api.go", "docs/guide.md"})
	if strings.Join(got, ",") != "main.go,docs/guide.md" {
		t.Errorf("searchable: %v", got)
	}
}
