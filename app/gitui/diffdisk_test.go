package gitui_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
)

// "A change on disk reloads an unchanged buffer" (TestEditorReloadsFromDisk), however the file is shown: a file open
// in the editor behind the diff, which the agent then changes, shows the agent's change in the diff, not the
// buffer's text from before it.
func TestDiffOfAFileOpenInTheEditorShowsAChangeOnDisk(t *testing.T) {
	s, r := explorerScene(t)
	const path = "internal/pricing/round.go"
	file := filepath.Join(r.Dir, path)
	s.Run("editor.open", registry.Args{"path": file})
	s.Quiet()
	s.Run("diff.open", registry.Args{"path": file})
	newText := func() ([]byte, bool) { return diffOf(s).NewText(path) }
	if !s.Headless.Until(5*time.Second, func() bool { _, ok := newText(); return ok }) {
		t.Fatal("round.go's diff was never made")
	}
	const agent = "// the agent's change"
	if err := os.WriteFile(file, []byte("package pricing\n\nfunc Round() {}\n"+agent+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !s.Headless.Until(5*time.Second, func() bool { b, ok := newText(); return ok && bytes.Contains(b, []byte(agent)) }) {
		b, _ := newText()
		t.Errorf("the diff still shows the editor's text from before the change on disk:\n%s", b)
	}
}
