package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/text"
)

// A file's text is read again when the file changes on disk or its buffer's version moves, and what's made from it
// is kept until then.
func TestFileSrcFollowsTheFileAndItsBuffer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.go")
	os.WriteFile(path, []byte("package a\n\nfunc A() {}\n"), 0o644)
	fs := fileSrcs{}
	f := fs.of(nil, path, true)
	ls, hl := f.lines(), f.around(registry.New(), path, 1, 0, 2)
	if !slices.Equal(ls, []string{"package a", "", "func A() {}"}) {
		t.Fatalf("lines = %q", ls)
	}
	if again := fs.of(nil, path, true); again.around(registry.New(), path, 1, 0, 2) != hl || &again.lines()[0] != &ls[0] {
		t.Error("an unchanged file was read or highlighted again")
	}

	os.WriteFile(path, []byte("package b\n"), 0o644)
	os.Chtimes(path, time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	if got := fs.of(nil, path, false).lines(); got[0] != "package a" {
		t.Errorf("without a stat, the file was read again: %q", got)
	}
	if got := fs.of(nil, path, true).lines(); !slices.Equal(got, []string{"package b"}) {
		t.Errorf("after the file changed, lines = %q", got)
	}

	buf := text.New([]byte("one\ntwo\n"), text.Author{})
	if got := f.load(buf, path, false).lines(); !slices.Equal(got, []string{"one", "two"}) {
		t.Errorf("the buffer's lines = %q", got)
	}
	buf.Apply(text.Edit{Ranges: []text.Replace{{Range: text.Range{Start: 0, End: 3}, Text: []byte("uno")}}})
	if got := f.load(buf, path, false).lines(); got[0] != "uno" {
		t.Errorf("after an edit, the buffer's lines = %q", got)
	}

	os.Remove(path)
	if f := fs.of(nil, path, true); !f.missing() {
		t.Error("a removed file isn't missing")
	}
}
