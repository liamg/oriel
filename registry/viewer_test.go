package registry_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
)

// plugins.md, file viewers: "the first viewer by `Order`, then name, whose `When` claims a file shows it"; one that
// panics is passed over.
func TestViewerForAFile(t *testing.T) {
	r := registry.New()
	png := func(p string) bool { return strings.HasSuffix(p, ".png") }
	r.Contribute(registry.FileViewer{Name: "boom", When: func(string) bool { panic("boom") }, View: "boom", Order: 1})
	r.Contribute(registry.FileViewer{Name: "image", When: png, View: "image", Order: 50})
	r.Contribute(registry.FileViewer{Name: "fancy", When: png, View: "fancy", Order: 10})
	r.Contribute(registry.FileViewer{Name: "media", When: func(p string) bool { return strings.HasSuffix(p, ".mp4") }, View: "media", Order: 50})
	if v, ok := registry.ViewerFor(r, "/a/b.png"); !ok || v.Name != "fancy" {
		t.Errorf("viewer for b.png = %v, %v", v.Name, ok)
	}
	if v, ok := registry.ViewerFor(r, "/a/b.mp4"); !ok || v.View != "media" {
		t.Errorf("viewer for b.mp4 = %v, %v", v.Name, ok)
	}
	if _, ok := registry.ViewerFor(r, "/a/b.go"); ok {
		t.Error("a viewer claimed a Go file")
	}
}
