package registry_test

import (
	"testing"

	"github.com/liamg/oriel/registry"
)

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
