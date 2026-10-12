package refcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/tools/layout"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func fakeSpec(t *testing.T) string {
	root := t.TempDir()
	write(t, root, "spec/vim/motion.txt", "Intro text.\n\n<S-Right>\tor\t\t*<S-Right>* *w*\n"+
		"w\t\t\t[count] words forward.  |exclusive|\n\t\t\tmotion.\n\n"+
		"<C-Right>\tor\t\t*<C-Right>* *W*\nW\t\t\t[count] WORDS forward.\n")
	write(t, root, "spec/nvim/motion.txt", "*w*\nnvim words forward.\n")
	write(t, root, "spec/lsp/spec.md", "# Spec\n\n## Text Documents\n\nPositions are zero-based.\n\n## Other\n\nElsewhere.\n")
	return root
}

func TestCheck(t *testing.T) {
	c := &Checker{Root: fakeSpec(t)}
	tests := []struct {
		ref, quote string
		ok         bool
	}{
		{"motion.txt *w*", "", true},
		{"motion.txt *w*", "[count] words forward.", true},
		{"motion.txt *w*", "words forward. |exclusive| motion.", true},
		{"vim/motion.txt *w*", "[count] words forward.", true},
		{"nvim/motion.txt *w*", "nvim words forward.", true},
		{"motion.txt *w*", "[count] WORDS forward.", false},
		{"motion.txt *b*", "", false},
		{"nope.txt *w*", "", false},
		{"lsp/spec.md#Text Documents", "Positions are zero-based.", true},
		{"lsp/spec.md#text-documents", "", true},
		{"lsp/spec.md#Text Documents", "Elsewhere.", false},
		{"lsp/spec.md#Missing", "", false},
		{"oracle", "", true},
		{"oracle nvim v0.11.7", "", true},
		{"made up", "", false},
	}
	for _, tt := range tests {
		err := c.Check(Ref{File: "x.vt", Line: 1, Ref: tt.ref, Quote: tt.quote})
		if (err == nil) != tt.ok {
			t.Errorf("Check(%q, %q) = %v, want ok=%v", tt.ref, tt.quote, err, tt.ok)
		}
	}
}

func TestScan(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "a.vt", "=== w\nref:    motion.txt *w*\nquote:  \"[count] \\\"words\\\" forward.\"\ngiven: |a b\n\n=== 2w\nref: oracle\n")
	refs, err := Scan(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].Ref != "motion.txt *w*" || refs[0].Quote != `[count] "words" forward.` || refs[0].Line != 2 || refs[1].Ref != "oracle" {
		t.Errorf("got %+v", refs)
	}
	p = write(t, dir, "a_test.go", "package a\n\n// ref: motion.txt *w*\n// quote: words forward\nfunc TestA() {}\n")
	if refs, err := Scan(p); err != nil || len(refs) != 1 || refs[0].Quote != "words forward" {
		t.Errorf("got %+v, %v", refs, err)
	}
	p = write(t, dir, "b.vt", "quote: \"orphan\"\n")
	if _, err := Scan(p); err == nil {
		t.Error("want an error for a quote with no ref")
	}
}

func TestRun(t *testing.T) {
	root := fakeSpec(t)
	write(t, root, "vim/testdata/good.vt", "ref: motion.txt *w*\nquote: \"[count] words forward.\"\n")
	write(t, root, "vim/testdata/bad.vt", "ref: motion.txt *nope*\n")
	write(t, root, "vendor/x/y.vt", "ref: motion.txt *ignored*\n")
	n, problems, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(problems) != 1 || !strings.Contains(problems[0], "*nope*") {
		t.Errorf("checked %d, problems %q", n, problems)
	}
}

func TestVendoredSpec(t *testing.T) {
	wd, _ := os.Getwd()
	root, err := layout.Root(wd)
	if err != nil {
		t.Fatal(err)
	}
	c := &Checker{Root: root}
	for _, r := range []Ref{
		{Ref: "motion.txt *w*", Quote: "[count] words forward."},
		{Ref: "change.txt *cw*", Quote: `Special case: When the cursor is in a word, "cw" and "cW" do not include the white space after a word`},
		{Ref: "nvim/motion.txt *w*", Quote: "[count] words forward."},
	} {
		if err := c.Check(r); err != nil {
			t.Error(err)
		}
	}
}
