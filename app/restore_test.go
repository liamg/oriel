package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/layout"
)

// SE-2: "restart restores the layout" (sessions.md: "the layout, open buffers and cursor positions come back"): the
// docks, the files open in it at their cursors, and the terminals, on the same directory.
func TestLayoutComesBackAfterARestart(t *testing.T) {
	state, dir := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("package b\n\nfunc B() {\n\treturn\n}\n"), 0o644)
	r := startApp(t, state, dir, nil)
	run := func(id string, args registry.Args) {
		t.Helper()
		if err := r.do(func() error { return r.host.Run(context.Background(), id, args) }); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	run("view.toggle", registry.Args{"id": ExplorerView, "place": "dock", "edge": "left"})
	run("term.open", registry.Args{"cmd": []string{"sh", "-c", "echo restored-term; exec cat"}})
	r.waitText("restored-term")
	run("editor.open", registry.Args{"path": filepath.Join(dir, "a.go")})
	run("editor.open", registry.Args{"path": filepath.Join(dir, "b.go")})
	r.typeKeys("3Gw")
	r.waitText("3:6")
	r.stop()

	r2 := startApp(t, state, dir, nil)
	r2.waitFrame("the layout back", func(s string) bool { return containsAll(s, "Explorer", "a.go", "b.go", "3:6", "restored-term") })
	var all []layout.ViewRef
	var left bool
	r2.do(func() error {
		root := r2.host.Layout()
		all, left = root.All(), root.Docks[layout.Left].Open
		return nil
	})
	ids := map[string]int{}
	for _, v := range all {
		ids[v.ID]++
	}
	if ids[EditorView] != 2 || ids[TerminalView] != 1 || ids[ExplorerView] != 1 || !left {
		t.Errorf("restored %v, left dock open %v", all, left)
	}
}
