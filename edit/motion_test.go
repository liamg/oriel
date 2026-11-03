package edit

import (
	"testing"

	"github.com/liamg/oriel/text"
)

// Typing at several cursors leaves each after its own text.
func TestReplaceMultiCursor(t *testing.T) {
	e := New(text.New([]byte("hello world"), text.Author{}), text.Author{Kind: text.User})
	e.SetCursors([]Cursor{{Off: 0}, {Off: 5}})
	if err := e.Replace("XY", 0); err != nil {
		t.Fatal(err)
	}
	if got := string(e.Buf().Bytes()); got != "XYhelloXY world" {
		t.Fatalf("text %q", got)
	}
	got := e.Cursors()
	if len(got) != 2 || got[0].Off != 2 || got[1].Off != 9 {
		t.Errorf("cursors %+v, want 2 and 9", got)
	}
}

// A selection is replaced, and the cursors keep their order.
func TestReplaceSelections(t *testing.T) {
	e := New(text.New([]byte("abc def"), text.Author{}), text.Author{Kind: text.User})
	e.SetCursors([]Cursor{{Off: 7, Anchor: 4, Selecting: true}, {Off: 0, Anchor: 3, Selecting: true}})
	if err := e.Replace("Z", 0); err != nil {
		t.Fatal(err)
	}
	if got := string(e.Buf().Bytes()); got != "Z Z" {
		t.Fatalf("text %q", got)
	}
	got := e.Cursors()
	if len(got) != 2 || got[0].Off != 3 || got[1].Off != 1 {
		t.Errorf("cursors %+v, want 3 and 1", got)
	}
}
