package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectTools(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  []Tool
	}{
		"go.mod toolchain wins": {map[string]string{"go.mod": "module x\n\ngo 1.22\n\ntoolchain go1.23.4\n"}, []Tool{{"go", "1.23.4"}}},
		"go.mod language":       {map[string]string{"go.mod": "module x\ngo 1.25\n"}, []Tool{{"go", "1.25"}}},
		"node from nvmrc":       {map[string]string{".nvmrc": "v20.11.1\n"}, []Tool{{"node", "20.11.1"}}},
		"node from engines":     {map[string]string{"package.json": `{"engines":{"node":">=18"}}`}, []Tool{{"node", "18"}}},
		"python project":        {map[string]string{"pyproject.toml": "requires-python = \">=3.11\"\n"}, []Tool{{"python", "3.11"}}},
		"tool-versions owns it": {map[string]string{".tool-versions": "golang 1.21.0\nnodejs 20\n", "go.mod": "go 1.25\n"}, []Tool{{"golang", "1.21.0"}, {"nodejs", "20"}}},
		"mise.toml tools":       {map[string]string{"mise.toml": "[env]\nX = \"1\"\n[tools]\ngo = \"1.24\"\nnode = [\"22\", \"20\"]\n"}, []Tool{{"go", "1.24"}, {"node", "22"}}},
		"junk is dropped":       {map[string]string{".tool-versions": "go 1.2;rm -rf /\n$(x) 1\n"}, nil},
	}
	for name, c := range cases {
		dir := t.TempDir()
		for f, body := range c.files {
			write(t, dir, f, body)
		}
		if got := DetectTools(dir); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}
