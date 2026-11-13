package notices

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tree(t *testing.T) (root, goroot string) {
	root, goroot = t.TempDir(), t.TempDir()
	write(t, goroot, "LICENSE", "go licence\n")
	write(t, root, "vendor/modules.txt", "# example.com/a v1.0.0\n## explicit\nexample.com/a\n# example.com/a/sub v0.1.0\nexample.com/a/sub\n")
	write(t, root, "vendor/example.com/a/LICENSE", "a licence\n")
	write(t, root, "vendor/example.com/a/icons/LICENSE", "icons licence\n")
	write(t, root, "vendor/example.com/a/code.go", "package a\n")
	write(t, root, "vendor/example.com/a/sub/LICENSE", "sub licence\n")
	write(t, root, "term/ghostty/licenses/ghostty.txt", "mit ghostty\n")
	write(t, root, "term/ghostty/licenses/uucode.txt", "mit uucode\n")
	write(t, root, "term/ghostty/licenses/simdutf.txt", "mit simdutf\n")
	write(t, root, "syntax/grammars/wasi/wasi-libc.txt", "mit wasi-libc\n")
	write(t, root, "syntax/grammars/wasi/musl.txt", "mit musl\n")
	write(t, root, "term/ghostty/licenses/highway.txt", "bsd highway\n")
	write(t, root, "voice/licenses/whisper.cpp.txt", "mit whisper.cpp\n")
	write(t, root, "syntax/grammars/manifest.tsv", "# comment\n"+
		"runtime\ttree-sitter/tree-sitter\tv0.27.0\tlib\tsum\tMIT\n"+
		"go\ttree-sitter/tree-sitter-go\tabc\t.\tsum\tMIT\n"+
		"hcl\tg/tree-sitter-hcl\tdef\t.\tsum\tApache-2.0\n"+
		"terraform\tg/tree-sitter-hcl\tdef\tdialects/terraform\tsum\tApache-2.0\n")
	write(t, root, "syntax/grammars/licenses/tree-sitter_tree-sitter.txt", "mit runtime\n")
	write(t, root, "syntax/grammars/licenses/tree-sitter_tree-sitter-go.txt", "mit go\n")
	write(t, root, "syntax/grammars/licenses/g_tree-sitter-hcl.txt", "apache\n")
	write(t, root, "syntax/queries/LICENSE", "apache\n")
	write(t, root, "syntax/queries/SOURCE", "source: https://github.com/nvim-treesitter/nvim-treesitter\ncommit: f00\n")
	write(t, root, "app/kit/gemoji/LICENSE", "mit gemoji\n")
	write(t, root, "app/kit/gemoji/SOURCE", "source: https://github.com/github/gemoji\ncommit: f00\n")
	write(t, root, "render/typeset/fonts/inter/OFL.txt", "ofl inter\n")
	write(t, root, "render/typeset/fonts/inter/SOURCE", "source: https://example.com/inter.zip\n")
	write(t, root, "theme/themes/oriel.toml", "# Oriel's built-in theme\nname = \"oriel\"\n")
	write(t, root, "theme/themes/nord.toml", "# Nord, by Arctic Ice Studio (MIT): nordtheme.com\nname = \"nord\"\n")
	write(t, root, "theme/themes/licenses/nord.txt", "mit nord\n")
	return root, goroot
}

func TestPartsCoverVendorAndEmbedded(t *testing.T) {
	root, goroot := tree(t)
	parts, err := Parts(root, goroot)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Part{}
	for _, p := range parts {
		got[p.Name] = p
	}
	want := map[string][]string{
		"Go":                                  {filepath.Join(goroot, "LICENSE")},
		"example.com/a v1.0.0":                {"vendor/example.com/a/LICENSE", "vendor/example.com/a/icons/LICENSE"},
		"example.com/a/sub v0.1.0":            {"vendor/example.com/a/sub/LICENSE"},
		"libghostty-vt":                       {"term/ghostty/licenses/ghostty.txt"},
		"uucode":                              {"term/ghostty/licenses/uucode.txt"},
		"simdutf":                             {"term/ghostty/licenses/simdutf.txt"},
		"wasi-libc":                           {"syntax/grammars/wasi/wasi-libc.txt", "syntax/grammars/wasi/musl.txt"},
		"Highway":                             {"term/ghostty/licenses/highway.txt"},
		"whisper.cpp and ggml":                {"voice/licenses/whisper.cpp.txt"},
		"tree-sitter runtime":                 {"syntax/grammars/licenses/tree-sitter_tree-sitter.txt"},
		"tree-sitter grammar: go":             {"syntax/grammars/licenses/tree-sitter_tree-sitter-go.txt"},
		"tree-sitter grammar: hcl, terraform": {"syntax/grammars/licenses/g_tree-sitter-hcl.txt"},
		"nvim-treesitter queries":             {"syntax/queries/LICENSE"},
		"gemoji (GitHub's emoji codes)":       {"app/kit/gemoji/LICENSE"},
		"Font: inter":                         {"render/typeset/fonts/inter/OFL.txt"},
		"Theme: Nord, by Arctic Ice Studio":   {"theme/themes/licenses/nord.txt"},
	}
	if len(got) != len(want) {
		t.Errorf("parts %v, want %d", names(parts), len(want))
	}
	for name, texts := range want {
		p, ok := got[name]
		if !ok {
			t.Errorf("no part %q in %v", name, names(parts))
			continue
		}
		if strings.Join(p.Texts, ",") != strings.Join(texts, ",") {
			t.Errorf("%s texts %v, want %v", name, p.Texts, texts)
		}
	}
	if l := got["tree-sitter grammar: hcl, terraform"].Licence; l != "Apache-2.0" {
		t.Errorf("hcl licence %q", l)
	}
	if p := got["Theme: Nord, by Arctic Ice Studio"]; p.Licence != "MIT" || p.Source != "nordtheme.com" {
		t.Errorf("nord theme %+v", p)
	}
	if s := got["nvim-treesitter queries"].Source; s != "https://github.com/nvim-treesitter/nvim-treesitter (f00)" {
		t.Errorf("queries source %q", s)
	}
}

func TestRenderPrintsEachTextOnce(t *testing.T) {
	root, goroot := tree(t)
	parts, err := Parts(root, goroot)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Render(root, parts)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if n := strings.Count(out, "apache\n"); n != 1 {
		t.Errorf("the Apache text appears %d times, want once:\n%s", n, out)
	}
	if !strings.Contains(out, "the same as tree-sitter grammar: hcl, terraform, above") {
		t.Errorf("the queries don't point at the text printed above:\n%s", out)
	}
	for _, s := range []string{"go licence", "icons licence", "mit ghostty", "ofl inter", "https://example.com/inter.zip"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q", s)
		}
	}
}

func TestRenderFailsOnAMissingText(t *testing.T) {
	root, goroot := tree(t)
	if err := os.Remove(filepath.Join(root, "syntax/grammars/licenses/tree-sitter_tree-sitter-go.txt")); err != nil {
		t.Fatal(err)
	}
	parts, err := Parts(root, goroot)
	if err == nil {
		_, err = Render(root, parts)
	}
	if err == nil {
		t.Error("a grammar with no licence file made notices")
	}
}

func TestAVendoredModuleWithoutALicenceFails(t *testing.T) {
	root, goroot := tree(t)
	if err := os.Remove(filepath.Join(root, "vendor/example.com/a/sub/LICENSE")); err != nil {
		t.Fatal(err)
	}
	if _, err := Parts(root, goroot); err == nil {
		t.Error("a vendored module with no licence made notices")
	}
}

func TestAThemeWithoutItsLicenceFails(t *testing.T) {
	root, goroot := tree(t)
	if err := os.Remove(filepath.Join(root, "theme/themes/licenses/nord.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := Parts(root, goroot); err == nil {
		t.Error("an attributed theme with no licence file made notices")
	}
}

func names(ps []Part) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}
