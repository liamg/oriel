package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ServerSpec is a language server: the languages it serves, how to start it, how to find a project's root,
// and what to tell it. The app registers these as lang.LSPServer (registry/lang) contributions; the two types have the
// same fields, so each converts to the other.
type ServerSpec struct {
	Name     string          // "gopls"; also the diagnostics' owner and the log source
	Language []string        // Oriel language names, as registry.Language names them: "go", "tsx"
	Cmd      []string        // the command and its arguments
	Roots    []string        // root markers, most important first: "go.work", "go.mod", ".git"
	Init     json.RawMessage // initializationOptions
	Settings json.RawMessage // answers workspace/configuration, and is sent as didChangeConfiguration
	Order    int             // among the servers for a language, lower is tried first
	Source   string          // who registered it
	// Connect, when set, reaches the server some other way than running Cmd (in process, over a socket), in place
	// of the client's launcher.
	Connect Launcher
}

// Serves reports whether the spec is for language.
func (s ServerSpec) Serves(language string) bool { return slices.Contains(s.Language, language) }

// FindRoot is the project root for a file: for each marker in order, the nearest directory above the file
// that has it. With no marker found, it's the file's directory.
func (s ServerSpec) FindRoot(path string) string {
	dir := filepath.Dir(path)
	for _, marker := range s.Roots {
		for d := dir; ; {
			if _, err := os.Stat(filepath.Join(d, marker)); err == nil {
				return d
			}
			up := filepath.Dir(d)
			if up == d {
				break
			}
			d = up
		}
	}
	return dir
}

// LanguageID is the LSP language identifier for an Oriel language name and file.
func LanguageID(language, path string) string {
	switch language {
	case "tsx":
		return "typescriptreact"
	case "javascript":
		if strings.HasSuffix(path, ".jsx") {
			return "javascriptreact"
		}
	case "bash":
		return "shellscript"
	case "c_sharp":
		return "csharp"
	case "gomod":
		return "go.mod"
	}
	return language
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// Builtins are the servers Oriel knows. Where two serve the same languages, the Manager starts the first one
// installed: typescript-language-server before vtsls, pyright before basedpyright.
func Builtins() []ServerSpec {
	const src = "builtin:lsp"
	return []ServerSpec{
		{Name: "gopls", Language: []string{"go", "gomod", "gowork", "gotmpl"}, Cmd: []string{"gopls"},
			Roots:    []string{"go.work", "go.mod", ".git"},
			Settings: raw(`{"gopls":{"semanticTokens":true,"hints":{"parameterNames":true,"assignVariableTypes":true,"compositeLiteralFields":true}}}`), Source: src},
		{Name: "typescript-language-server", Language: tsLanguages, Cmd: []string{"typescript-language-server", "--stdio"},
			Roots: tsRoots, Settings: raw(tsSettings), Source: src},
		{Name: "vtsls", Language: tsLanguages, Cmd: []string{"vtsls", "--stdio"}, Roots: tsRoots, Settings: raw(tsSettings), Order: 1, Source: src},
		{Name: "pyright", Language: []string{"python"}, Cmd: []string{"pyright-langserver", "--stdio"}, Roots: pyRoots, Source: src},
		{Name: "basedpyright", Language: []string{"python"}, Cmd: []string{"basedpyright-langserver", "--stdio"}, Roots: pyRoots, Order: 1, Source: src},
		{Name: "rust-analyzer", Language: []string{"rust"}, Cmd: []string{"rust-analyzer"},
			Roots: []string{"Cargo.toml", "rust-project.json", ".git"}, Source: src},
		{Name: "clangd", Language: []string{"c", "cpp", "objc", "objcpp", "cuda"}, Cmd: []string{"clangd"},
			Roots: []string{"compile_commands.json", "compile_flags.txt", ".clangd", ".git"}, Source: src},
		{Name: "lua-language-server", Language: []string{"lua"}, Cmd: []string{"lua-language-server"},
			Roots: []string{".luarc.json", ".luarc.jsonc", ".stylua.toml", "stylua.toml", ".git"}, Source: src},
		{Name: "yaml-language-server", Language: []string{"yaml"}, Cmd: []string{"yaml-language-server", "--stdio"},
			Roots: []string{".git"}, Source: src},
		{Name: "vscode-json-language-server", Language: []string{"json", "jsonc"}, Cmd: []string{"vscode-json-language-server", "--stdio"},
			Roots: []string{".git"}, Init: raw(`{"provideFormatter":true}`), Source: src},
		{Name: "bash-language-server", Language: []string{"bash", "sh"}, Cmd: []string{"bash-language-server", "start"},
			Roots: []string{".git"}, Source: src},
		{Name: "terraform-ls", Language: []string{"terraform", "hcl"}, Cmd: []string{"terraform-ls", "serve"},
			Roots: []string{".terraform", ".git"}, Source: src},
	}
}

var (
	tsLanguages = []string{"typescript", "tsx", "javascript"}
	tsRoots     = []string{"tsconfig.json", "jsconfig.json", "package.json", ".git"}
	pyRoots     = []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", "pyrightconfig.json", ".git"}
)

const tsSettings = `{"typescript":{"inlayHints":{"includeInlayParameterNameHints":"all","includeInlayVariableTypeHints":true,` +
	`"includeInlayFunctionLikeReturnTypeHints":true}},"javascript":{"inlayHints":{"includeInlayParameterNameHints":"all"}}}`

// section answers one workspace/configuration item: the settings under a dotted section, the whole settings
// for none, and null where there's nothing.
func section(settings json.RawMessage, name string) json.RawMessage {
	cur := settings
	if len(cur) == 0 {
		return raw("null")
	}
	if name == "" {
		return cur
	}
	for _, part := range strings.Split(name, ".") {
		var m map[string]json.RawMessage
		if json.Unmarshal(cur, &m) != nil {
			return raw("null")
		}
		next, ok := m[part]
		if !ok {
			return raw("null")
		}
		cur = next
	}
	return cur
}
