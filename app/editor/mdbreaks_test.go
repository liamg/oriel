package editor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
)

// A document's newline inside a paragraph is a soft break, a space, while a comment's is a line break: the same
// lines read as one in the preview and as two in a thread on it.
//
// ref:   gfm/spec.md#Soft line breaks
// quote: "A renderer may also provide an option to render soft line breaks as hard line breaks."
// ref:   github-docs/basic-writing-and-formatting-syntax.md#Line breaks
// quote: "However, if you are writing in an .md file, the example above would render on one line without a line break."
func TestDocumentsJoinSoftBreaks(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# Notes\n\nTies go to even\nin every currency.\n"), 0o644)
	s := threadsScene(t, dir, [4]string{"notes.md", "3", "Ties go to even", "Negatives too\nand zero."})
	s.Run("editor.open", registry.Args{"path": "notes.md"})
	s.Frames(3)
	s.Click(s.TextPoint("Preview"), 0)
	s.Frames(4)
	if text := s.Text(); !apptest.HasAll(text, "Ties go to even in every currency.", "\nNegatives too\nand zero.\n") || strings.Contains(text, "Negatives too and zero.") {
		t.Errorf("want the document's lines joined and the comment's kept:\n%s", text)
	}
}
