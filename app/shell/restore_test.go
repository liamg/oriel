package shell_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/layout"
)

// A restart restores the layout, open buffers and cursor positions: the docks, the files open in it at their cursors, and the terminals, on the same directory.
func TestLayoutComesBackAfterARestart(t *testing.T) {
	state, dir := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("package b\n\nfunc B() {\n\treturn\n}\n"), 0o644)
	r := apptest.NewWindow(t, apptest.WindowOpts{State: state, WorkDir: dir})
	run := func(id string, args registry.Args) {
		t.Helper()
		if err := r.Do(func() error { return r.Host.Run(context.Background(), id, args) }); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	run("view.toggle", registry.Args{"id": kit.ExplorerView, "place": "dock", "edge": "left"})
	run("term.open", registry.Args{"cmd": []string{"sh", "-c", "echo restored-term; exec cat"}})
	r.WaitText("restored-term")
	run("editor.open", registry.Args{"path": filepath.Join(dir, "a.go")})
	run("editor.open", registry.Args{"path": filepath.Join(dir, "b.go")})
	r.TypeKeys("3Gw")
	r.WaitText("3:6")
	r.Stop()

	r2 := apptest.NewWindow(t, apptest.WindowOpts{State: state, WorkDir: dir})
	r2.WaitFrame("the layout back", func(s string) bool { return apptest.HasAll(s, "EXPLORER", "a.go", "b.go", "3:6", "restored-term") })
	var all []layout.ViewRef
	var left bool
	r2.Do(func() error {
		root := r2.Host.Layout()
		all, left = root.All(), root.Docks[layout.Left].Open
		return nil
	})
	ids := map[string]int{}
	for _, v := range all {
		ids[v.ID]++
	}
	if ids[kit.EditorView] != 2 || ids[kit.TerminalView] != 1 || ids[kit.ExplorerView] != 1 || !left {
		t.Errorf("restored %v, left dock open %v", all, left)
	}
}
