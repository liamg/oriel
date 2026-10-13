package syntax

import (
	"embed"
	"io/fs"
	"path"
)

//go:embed grammars/*.wasm
var grammarFS embed.FS

//go:embed queries
var queryFS embed.FS

// builtins are the languages Oriel ships, registered as a plugin's would be.
var builtins = []struct {
	name string
	exts []string
}{
	{"go", []string{".go"}},
	{"typescript", []string{".ts", ".mts", ".cts"}},
	{"python", []string{".py", ".pyi"}},
	{"rust", []string{".rs"}},
	{"lua", []string{".lua"}},
	{"markdown", []string{".md", ".markdown"}},
	{"markdown_inline", nil},
}

func init() {
	inheritedOnly["ecma"] = queriesOf("ecma")
	for _, b := range builtins {
		wasm, err := grammarFS.ReadFile("grammars/" + b.name + ".wasm")
		if err != nil {
			panic(err)
		}
		Register(&Language{Name: b.name, Wasm: wasm, Extensions: b.exts, Queries: queriesOf(b.name)})
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
