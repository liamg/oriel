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
	write(t, root, "spec/vim/testdir/test_x.vim", "\" header\nfunc Test_one()\n  normal! dw\nendfunc\n\nfunc! Test_two()\n  normal! x\nendfunc\n")
	write(t, root, "spec/lsp/spec.md", "# Spec\n\n## Text Documents\n\nPositions are zero-based.\n\n## Other\n\nElsewhere.\n\n### `--testNamePattern=<regex>`\n\nRun only tests that match.\n")
	write(t, root, "spec/lsp/hover.md", "#### <a href=\"#textDocument_hover\" name=\"textDocument_hover\" class=\"anchor\">Hover Request (:leftwards_arrow_with_hook:)</a>\n\nHover info.\n\n#### <a href=\"#next\" name=\"next\" class=\"anchor\">Next</a>\n\nElsewhere.\n")
	write(t, root, "spec/css/Overview.bs", "<h2 id=\"mix\">Mixing</h2>\n<p>Mix &amp; <em>match</em>\n colours.\n<h3 id=\"sub\">Sub</h3>\nStill mixing.\n<h2 id='next'>Next</h2>\nElsewhere.\nset |x| to 1, giving&nbsp;''red'' or `blue`\n")
	write(t, root, "spec/wcag/terms.html", "<dt><dfn id=\"dfn-ratio\">ratio</dfn></dt>\n<dd>\n  <p>(L1 + 0.05) /\n (L2 + 0.05)</p>\n</dd>\n<dt><dfn id=\"dfn-other\">other</dfn></dt>\n<dd>Elsewhere.</dd>\n")
	write(t, root, "spec/wcag/sc.html", "<section id=\"min\" class=\"sc\">\n<h4>Minimum</h4>\n<p>at least 4.5:1</p>\n<section id=\"inner\">Inner.</section>\n</section>\nAfter.\n")
	write(t, root, "spec/ec/index.rst", "Title\n^^^^^\n\nFile Format\n===========\n\nRead ``one`` line\nat a time.\n\nSub\n---\n\nStill format.\n\nGlobs\n=====\n\nElsewhere.\n")
	write(t, root, "spec/git/attrs.txt", "gitattributes(5)\n================\n\nEffects\n-------\n\n`text`\n^^^^^^\n\nThis attribute marks the path as a text file.\n\nOther\n-----\n\nElsewhere.\n")
	write(t, root, "spec/go/help.txt", "The flags:\n\n\t-run regexp\n\t    Run only those tests matching\n\t    the regular expression.\n")
	write(t, root, "spec/go/src/match.go", "package testing\n\n// rewrite rewrites a subname to having only printable characters and no white\n// space.\nfunc rewrite(s string) string {\n\treturn s\n}\n\n// unique creates a unique name.\nfunc (m *matcher) unique(parent, subname string) string {\n\treturn parent\n}\n\ntype matcher struct {\n\tfilter filterMatch\n}\n")
	write(t, root, "spec/css/code.js", "// header\nfunction lin(RGB) {\n\treturn val / 12.92;\n}\n\nfunction gam(RGB) {\n\treturn 1.055;\n}\n")
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
		{"lsp/spec.md#--testnamepatternregex", "Run only tests that match.", true},
		{"lsp/hover.md#textDocument_hover", "Hover info.", true},
		{"lsp/hover.md#Hover Request (:leftwards_arrow_with_hook:)", "Hover info.", true},
		{"lsp/hover.md#textDocument_hover", "Elsewhere.", false},
		{"lsp/hover.md#anchor", "", false},
		{"css/Overview.bs#mix", "Mix & match colours.", true},
		{"css/Overview.bs#mix", "Still mixing.", true},
		{"css/Overview.bs#mix", "Elsewhere.", false},
		{"css/Overview.bs#next", "set x to 1, giving red or blue", true},
		{"css/Overview.bs#sub", "Still mixing.", true},
		{"css/Overview.bs#next", "", true},
		{"css/Overview.bs#gone", "", false},
		{"wcag/terms.html#dfn-ratio", "(L1 + 0.05) / (L2 + 0.05)", true},
		{"wcag/terms.html#dfn-ratio", "Elsewhere.", false},
		{"wcag/terms.html#dfn-other", "Elsewhere.", true},
		{"wcag/sc.html#min", "at least 4.5:1", true},
		{"wcag/sc.html#min", "Inner.", true},
		{"wcag/sc.html#min", "After.", false},
		{"ec/index.rst#File Format", "Read one line at a time.", true},
		{"ec/index.rst#File Format", "Still format.", true},
		{"ec/index.rst#File Format", "Elsewhere.", false},
		{"ec/index.rst#Globs", "Elsewhere.", true},
		{"ec/index.rst#Missing", "", false},
		{"git/attrs.txt#text", "This attribute marks the path as a text file.", true},
		{"git/attrs.txt#Effects", "This attribute marks the path", true},
		{"git/attrs.txt#text", "Elsewhere.", false},
		{"css/code.js#lin", "return val / 12.92;", true},
		{"css/code.js#lin", "return 1.055;", false},
		{"css/code.js#gam", "", true},
		{"css/code.js#cube", "", false},
		{"testdir/test_x.vim#Test_one", "", true},
		{"testdir/test_x.vim#Test_one", "normal! dw", true},
		{"vim/testdir/test_x.vim#Test_two", "normal! x", true},
		{"testdir/test_x.vim#Test_one", "normal! x", false},
		{"testdir/test_x.vim#Test_three", "", false},
		{"testdir/test_y.vim#Test_one", "", false},
		{"go/help.txt", "Run only those tests matching the regular expression.", true},
		{"go/help.txt", "", true},
		{"go/help.txt", "Run every test.", false},
		{"go/nope.txt", "", false},
		{"go/src/match.go#rewrite", "rewrite rewrites a subname to having only printable characters and no white space.", true},
		{"go/src/match.go#rewrite", "return s", true},
		{"go/src/match.go#rewrite", "unique creates", false},
		{"go/src/match.go#matcher.unique", "unique creates a unique name.", true},
		{"go/src/match.go#unique", "return parent", true},
		{"go/src/match.go#other.unique", "", false},
		{"go/src/match.go#matcher", "filter filterMatch", true},
		{"go/src/match.go#missing", "", false},
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
		{Ref: "nvim/change.txt *cw*", Quote: `Special case: When the cursor is in a word, "cw" and "cW" do not include the white space after a word`},
	} {
		if err := c.Check(r); err != nil {
			t.Error(err)
		}
	}
}
