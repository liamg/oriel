package apigen

import (
	"os"
	"path/filepath"
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
