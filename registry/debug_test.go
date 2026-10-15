package registry_test

import (
	"testing"

	"github.com/liamg/oriel/dap"
	"github.com/liamg/oriel/registry"
)

// DB-1: debug adapters are contributions; a later one of a name shadows the earlier until disposed.
func TestDebugAdapters(t *testing.T) {
	r := registry.New()
	for _, s := range dap.Builtins() {
		r.Contribute(registry.DebugAdapter(s))
	}
	mine := r.Contribute(registry.DebugAdapter{Name: "dlv", Type: "go", Cmd: []string{"my-dlv"}, Source: "config"})
	var dlv registry.DebugAdapter
	for _, a := range registry.DebugAdapters(r) {
		if a.Name == "dlv" {
			dlv = a
		}
	}
	if len(registry.DebugAdapters(r)) != len(dap.Builtins()) || dlv.Cmd[0] != "my-dlv" || dap.AdapterSpec(dlv).Source != "config" {
		t.Errorf("dlv = %+v, want the shadowing one", dlv)
	}
	mine.Dispose()
	for _, a := range registry.DebugAdapters(r) {
		if a.Name == "dlv" && a.Cmd[0] != "dlv" {
			t.Errorf("after dispose dlv = %+v", a)
		}
	}
}

func TestGutterActionFor(t *testing.T) {
	r := registry.New()
	if _, ok := registry.GutterActionFor(r, "/a.go"); ok {
		t.Fatal("an action with none registered")
	}
	r.Contribute(registry.GutterAction{Name: "late", Command: "b", Order: 20})
	r.Contribute(registry.GutterAction{Name: "go", Command: "a", Order: 10, When: func(p string) bool { return p == "/a.go" }})
	if a, _ := registry.GutterActionFor(r, "/a.go"); a.Command != "a" {
		t.Errorf("a.go: %+v, want the lower order", a)
	}
	if a, _ := registry.GutterActionFor(r, "/b.txt"); a.Command != "b" {
		t.Errorf("b.txt: %+v, want the one for every file", a)
	}
}
