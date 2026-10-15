package apigen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What's committed is what the annotations generate: run go generate ./script/api when this fails.
func TestGeneratedIsCurrent(t *testing.T) {
	root := filepath.Join("..", "..")
	files, err := Generate(root, filepath.Join(root, "script", "api"), "0.4.0")
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range files {
		got, err := os.ReadFile(p)
		if err != nil || string(got) != string(want) {
			t.Errorf("%s is stale; run go generate ./script/api", p)
		}
	}
}

func TestLuaName(t *testing.T) {
	for in, want := range map[string]string{"FindAll": "find_all", "JSON": "json", "Set": "set", "Statusline": "statusline"} {
		if got := luaName(in); got != want {
			t.Errorf("luaName(%s) = %s", in, got)
		}
	}
}

// security.md: "an API without a declared capability fails go generate, so no unguarded door can be added by
// accident". A namespace's cap covers its functions; a function's overrides it.
func TestCapabilityRequired(t *testing.T) {
	dir := t.TempDir()
	src := `package api

//oriel:api name=x
type X struct{}

//oriel:api doc="d"
func (X) Open() {}
`
	os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o644)
	if _, err := Parse(dir); err == nil || !strings.Contains(err.Error(), "declares no capability") {
		t.Errorf("a function with no capability: %v", err)
	}
	src = strings.Replace(src, "name=x", "name=x cap=ui", 1) + `
//oriel:api doc="d" cap=exec
func (X) Run() {}
`
	os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o644)
	nss, err := Parse(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind("api", nss)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`a.check("ui")`, `a.check("exec")`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the glue doesn't check %s:\n%s", want, b)
		}
	}
}
