package kit

import (
	"testing"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/text"
)

// Applying a diff's hunks to a buffer gives the buffer of the new file, however the change meets the buffer's
// ends.
func TestHunkEdits(t *testing.T) {
	for _, c := range []struct{ old, new string }{
		{"a\nb\nc\n", "a\nB\nc\n"},
		{"a\nb\nc\n", "a\nb\nc\nd\n"},
		{"a\nb\nc\n", "z\na\nb\nc\n"},
		{"a\nb\nc\n", "a\nb\n"},
		{"a\nb\nc\n", "b\nc\n"},
		{"a\nb\nc\n", "x\n"},
		{"a\r\nb\r\nc\r\n", "a\r\nB\r\nc\r\nd\r\n"},
		{"a\nb\nc\nd\ne\n", "A\nb\nC\nd\nE\nf\n"},
	} {
		text0, format := edit.Decode([]byte(c.old))
		buf := text.New(text0, text.Author{})
		snap := buf.Snapshot()
		if _, err := buf.Apply(text.Edit{Ranges: HunkEdits(snap, gitx.DiffLines([]byte(c.old), []byte(c.new)))}); err != nil {
			t.Fatalf("%q → %q: %v", c.old, c.new, err)
		}
		if got := string(edit.Encode(buf.Snapshot().Bytes(), format, false)); got != c.new {
			t.Errorf("%q → %q: got %q", c.old, c.new, got)
		}
	}
}
