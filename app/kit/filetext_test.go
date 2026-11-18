package kit

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liamg/oriel/internal/waitfor"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/text"
)

// testReads are reads with a registry serving them, closed as the test ends.
func testReads(t *testing.T) (*Reads, registry.Registry) {
	t.Helper()
	rd := NewReads(ReadsOpts{})
	t.Cleanup(rd.Close)
	r := registry.New()
	r.Contribute(registry.Service{Name: ReadsService, Impl: rd})
	return rd, r
}

// A file's text is read off the loop: the first ask finds it loading, a later one has it.
func TestFileTextReadsOffTheLoop(t *testing.T) {
	_, r := testReads(t)
	path := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(path, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := FileTexts{}
	if f := fs.Of(r, path); !f.Loading() || f.Src != nil {
		t.Fatalf("the first ask read the file on the loop: %q", f.Src)
	}
	waitfor.Poll(t, "the file's text", func() bool { return !fs.Of(r, path).Loading() })
	if got := fs.Of(r, path).Lines(); !slices.Equal(got, []string{"package a"}) {
		t.Errorf("lines = %q", got)
	}
}

// A preview's highlighting is made off the loop: plain text until it's in.
func TestFileTextHighlightsOffTheLoop(t *testing.T) {
	_, r := testReads(t)
	path := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(path, []byte("package a\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := FileTexts{}
	waitfor.Poll(t, "the file's text", func() bool { return !fs.Of(r, path).Loading() })
	f := fs.Of(r, path)
	if hl := f.Around(r, path, 3, 1, 1); f.hlDone || len(hl.lines) == 0 {
		t.Fatalf("the first frame highlighted on the loop, or showed %q", hl.lines)
	}
	waitfor.Poll(t, "the highlighting", func() bool { f.Around(r, path, 3, 1, 1); return f.hlDone })
}

// A file's text is read again when the file changes on disk or its buffer's version moves, and what's made from it
// is kept until then.
func TestFileTextFollowsTheFileAndItsBuffer(t *testing.T) {
	rd, r := testReads(t)
	path := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(path, []byte("package a\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := FileTexts{}
	waitfor.Poll(t, "the file's text", func() bool { return !fs.Of(r, path).Loading() })
	f := fs.Of(r, path)
	waitfor.Poll(t, "the highlighting", func() bool { f.Around(registry.New(), path, 1, 0, 2); return f.hlDone })
	ls, hl := f.Lines(), f.Around(registry.New(), path, 1, 0, 2)
	if !slices.Equal(ls, []string{"package a", "", "func A() {}"}) {
		t.Fatalf("lines = %q", ls)
	}
	if again := fs.Of(r, path); again.Around(registry.New(), path, 1, 0, 2) != hl || &again.Lines()[0] != &ls[0] {
		t.Error("an unchanged file was read or highlighted again")
	}

	if err := os.WriteFile(path, []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitfor.Poll(t, "the changed file", func() bool { return slices.Equal(fs.Of(r, path).Lines(), []string{"package b"}) })

	buf := text.New([]byte("one\ntwo\n"), text.Author{})
	if got := f.Load(rd, buf, path).Lines(); !slices.Equal(got, []string{"one", "two"}) {
		t.Errorf("the buffer's lines = %q", got)
	}
	if _, err := buf.Apply(text.Edit{Ranges: []text.Replace{{Range: text.Range{Start: 0, End: 3}, Text: []byte("uno")}}}); err != nil {
		t.Fatal(err)
	}
	if got := f.Load(rd, buf, path).Lines(); got[0] != "uno" {
		t.Errorf("after an edit, the buffer's lines = %q", got)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitfor.Poll(t, "the file missing", func() bool { return fs.Of(r, path).Missing() })
}
