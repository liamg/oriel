package syntax

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io"
	"io/fs"
	"path"
	"sync"
)

//go:embed grammars/*.wasm.gz
var grammarFS embed.FS

//go:embed queries
var queryFS embed.FS

// core are the languages Oriel ships, which builtin:languages registers as a plugin would: name, file
// extensions, whole file names and #! interpreters. One with no grammar file is queries only, for inheriting.
var core = []struct {
	name                   string
	exts, filenames, bangs []string
	queriesOnly            bool
}{
	{name: "ecma", queriesOnly: true},
	{name: "jsx", queriesOnly: true},
	{name: "html_tags", queriesOnly: true},
	{name: "go", exts: []string{".go"}},
	{name: "typescript", exts: []string{".ts", ".mts", ".cts"}},
	{name: "tsx", exts: []string{".tsx"}},
	{name: "javascript", exts: []string{".js", ".mjs", ".cjs", ".jsx"}, bangs: []string{"node"}},
	{name: "python", exts: []string{".py", ".pyi"}, bangs: []string{"python", "python3"}},
	{name: "rust", exts: []string{".rs"}},
	{name: "lua", exts: []string{".lua"}, bangs: []string{"lua", "luajit"}},
	{name: "markdown", exts: []string{".md", ".markdown"}},
	{name: "markdown_inline"},
	{name: "c", exts: []string{".c", ".h"}},
	{name: "cpp", exts: []string{".cc", ".cpp", ".cxx", ".c++", ".hh", ".hpp", ".hxx"}},
	{name: "java", exts: []string{".java"}},
	{name: "ruby", exts: []string{".rb", ".rake", ".gemspec"}, filenames: []string{"Gemfile", "Rakefile"}, bangs: []string{"ruby"}},
	{name: "bash", exts: []string{".sh", ".bash"}, filenames: []string{".bashrc", ".bash_profile", ".profile"}, bangs: []string{"bash", "sh"}},
	{name: "json", exts: []string{".json"}},
	{name: "yaml", exts: []string{".yaml", ".yml"}},
	{name: "toml", exts: []string{".toml"}, filenames: []string{"Cargo.lock"}},
	{name: "html", exts: []string{".html", ".htm"}},
	{name: "css", exts: []string{".css"}},
	{name: "sql", exts: []string{".sql"}},
	{name: "dockerfile", exts: []string{".dockerfile"}, filenames: []string{"Dockerfile", "Containerfile"}},
	{name: "make", exts: []string{".mk"}, filenames: []string{"Makefile", "makefile", "GNUmakefile"}},
	{name: "diff", exts: []string{".diff", ".patch"}},
	{name: "gitcommit", filenames: []string{"COMMIT_EDITMSG"}},
	{name: "vimdoc"},
	{name: "hcl", exts: []string{".hcl", ".nomad"}, filenames: []string{".terraformrc", "terraform.rc"}},
	{name: "terraform", exts: []string{".tf", ".tfvars", ".tofu", ".tofuvars"}},
	{name: "kotlin", exts: []string{".kt", ".kts"}},
	{name: "c_sharp", exts: []string{".cs", ".csx"}},
	{name: "php_only", queriesOnly: true},
	{name: "php", exts: []string{".php", ".phtml"}, bangs: []string{"php"}},
	{name: "zig", exts: []string{".zig", ".zon"}},
	{name: "nix", exts: []string{".nix"}},
	{name: "proto", exts: []string{".proto"}},
	{name: "graphql", exts: []string{".graphql", ".graphqls", ".gql"}},
	{name: "xml", exts: []string{".xml", ".xsd", ".xsl", ".xslt", ".svg", ".plist"}},
	{name: "ini", exts: []string{".ini"}},
	{name: "elixir", exts: []string{".ex", ".exs"}, filenames: []string{"mix.lock"}, bangs: []string{"elixir"}},
	{name: "scala", exts: []string{".scala", ".sc", ".sbt"}},
	{name: "gitignore", filenames: []string{".gitignore", ".dockerignore", ".ignore"}},
	{name: "vue", exts: []string{".vue"}},
	{name: "svelte", exts: []string{".svelte"}},
}

var builtin struct {
	once sync.Once
	set  *Set
}

// Builtin is Oriel's core languages, each grammar decompressed on first use.
func Builtin() *Set {
	builtin.once.Do(func() {
		var ls []*Language
		for _, c := range core {
			l := &Language{Name: c.name, Extensions: c.exts, Filenames: c.filenames, Shebangs: c.bangs, Queries: queriesOf(c.name)}
			if !c.queriesOnly {
				l.Grammar = grammar(c.name)
			}
			ls = append(ls, l)
		}
		builtin.set = NewSet(ls...)
	})
	return builtin.set
}

// Languages are the set's languages, in order.
func (s *Set) Languages() []*Language { return s.langs }

// grammar reads an embedded grammar, decompressing it.
func grammar(name string) func() ([]byte, error) {
	return func() ([]byte, error) {
		gz, err := grammarFS.ReadFile("grammars/" + name + ".wasm.gz")
		if err != nil {
			return nil, err
		}
		r, err := gzip.NewReader(bytes.NewReader(gz))
		if err != nil {
			return nil, err
		}
		return io.ReadAll(r)
	}
}

func queriesOf(lang string) map[string]string {
	out := map[string]string{}
	entries, _ := fs.ReadDir(queryFS, "queries/"+lang)
	for _, e := range entries {
		b, err := queryFS.ReadFile("queries/" + lang + "/" + e.Name())
		if err == nil {
			out[e.Name()[:len(e.Name())-len(path.Ext(e.Name()))]] = string(b)
		}
	}
	return out
}
