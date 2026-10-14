package settingsfile

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/liamg/oriel/toml"
)

// settings.md, "Project trust": a project file applies once trusted, by its SHA-256 in trusted.toml, so a change
// needs trusting again.
func TestTrust(t *testing.T) {
	dir := t.TempDir()
	tr := Trust{Path: filepath.Join(dir, "conf", "trusted.toml")}
	file := filepath.Join(dir, "repo", ".oriel", "settings.toml")
	src := []byte("[sandbox]\negress = [\"allow example.com\"]\n")
	if tr.Trusted(file, src) {
		t.Fatal("trusted before it was")
	}
	if err := tr.Add(file, src); err != nil {
		t.Fatal(err)
	}
	if !tr.Trusted(file, src) {
		t.Error("not trusted after Add")
	}
	if tr.Trusted(file, append(src, '#')) {
		t.Error("a changed file is still trusted")
	}
	other := filepath.Join(dir, "other", ".oriel", "settings.toml")
	if err := tr.Add(other, nil); err != nil {
		t.Fatal(err)
	}
	if !tr.Trusted(file, src) || !tr.Trusted(other, nil) {
		t.Error("trusting a second file lost the first")
	}
	b, _ := os.ReadFile(tr.Path)
	if _, err := toml.Parse(b); err != nil {
		t.Errorf("trusted.toml doesn't parse: %v\n%s", err, b)
	}
}

// A file's keys are option names, from tables or dotted keys; a table under a name leaf says is a value stays whole.
func TestKeys(t *testing.T) {
	doc, err := toml.Parse([]byte("[sandbox]\negress = [\"allow a\"]\nservers = { github = \"https://x\" }\n[sandbox.more]\nlinear = \"npx linear\"\n[ui]\ntheme = \"oriel\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	keys := Keys(doc.Value(), func(name string) bool { return name == "sandbox.servers" || name == "sandbox.more" })
	var names []string
	for _, k := range keys {
		names = append(names, k.Name)
	}
	if want := []string{"sandbox.egress", "sandbox.more", "sandbox.servers", "ui.theme"}; !reflect.DeepEqual(names, want) {
		t.Errorf("keys %v, want %v", names, want)
	}
	if m, ok := StringMap(keys[1].Value); !ok || m["linear"] != "npx linear" {
		t.Errorf("sandbox.more = %v", keys[1].Value)
	}
	if l, ok := Strings(keys[0].Value); !ok || !reflect.DeepEqual(l, []string{"allow a"}) {
		t.Errorf("sandbox.egress = %v", keys[0].Value)
	}
	if _, ok := StringMap(map[string]any{"a": 1}); ok {
		t.Error("a table of numbers is a map of strings")
	}
}
