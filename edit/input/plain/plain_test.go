package plain

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/text"
)

// show writes a buffer with its cursor as "|" and a selection in "[" "]".
func show(ed *edit.Editor) string {
	s := ed.Buf().String()
	c := ed.Cursors()[0]
	if r, ok := c.Selection(); ok {
		return s[:r.Start] + "[" + s[r.Start:r.End] + "]" + s[r.End:]
	}
	return s[:c.Off] + "|" + s[c.Off:]
}

func load(s string) *edit.Editor {
	off := strings.Index(s, "|")
	ed := edit.New(text.New([]byte(strings.Replace(s, "|", "", 1)), text.Author{}), text.Author{Kind: text.User})
	ed.SetCursors([]edit.Cursor{{Off: off}})
	return ed
}

type clip struct{ s string }

func (c *clip) Read(bool) (string, bool) { return c.s, c.s != "" }
func (c *clip) Write(_ bool, s string)   { c.s = s }

// The plain engine: CUA keys (⌘C, ⌘V, shift-select), for people who don't use vim.
func TestPlainKeys(t *testing.T) {
	for _, tt := range []struct{ name, given, keys, want string }{
		{"typing inserts", "ab|c", "xy", "abxy|c"},
		{"space and enter", "a|b", "<Space><CR>", "a \n|b"},
		{"backspace", "abc|", "<BS><BS>", "a|"},
		{"delete", "|abc", "<Del>", "|bc"},
		{"option-backspace deletes a word", "foo bar|", "<M-BS>", "foo |"},
		{"command-backspace deletes to the line start", "x\nfoo bar|", "<D-BS>", "x\n|"},
		{"arrows move", "a|bc", "<Right><Right><Left>", "ab|c"},
		{"up and down keep the column", "abcd\nx\nabcd|", "<Up><Up>", "abcd|\nx\nabcd"},
		{"shift selects", "a|bcd", "<S-Right><S-Right>", "a[bc]d"},
		{"typing replaces the selection", "a|bcd", "<S-Right><S-Right>X", "aX|d"},
		{"left collapses a selection to its start", "a|bcd", "<S-Right><S-Right><Left>", "a|bcd"},
		{"option-arrows move by word", "|foo bar", "<M-Right>", "foo| bar"},
		{"command-arrows go to the line's ends", "x\nfo|o\ny", "<D-Right>", "x\nfoo|\ny"},
		{"home and end", "fo|o", "<Home>", "|foo"},
		{"select all", "a|b\nc", "<D-a>", "[ab\nc]"},
		{"undo takes back the typing as one", "|", "abc<D-z>", "|"},
		{"redo", "|", "abc<D-z><D-S-z>", "abc|"},
		{"ctrl works as command does", "a|b", "<C-a>X", "X|"},
	} {
		ed := load(tt.given)
		ed.SetClipboard(&clip{})
		st := Engine{}.Attach(ed)
		for _, k := range keymap.Parse(tt.keys) {
			if r := st.Key(k); !r.Handled {
				t.Errorf("%s: %s not handled", tt.name, k)
			}
		}
		if got := show(ed); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestPlainClipboard(t *testing.T) {
	ed := load("one |two")
	c := &clip{}
	ed.SetClipboard(c)
	st := Engine{}.Attach(ed)
	for _, k := range keymap.Parse("<M-S-Right><D-c><D-v><D-v>") {
		st.Key(k)
	}
	if c.s != "two" || show(ed) != "one twotwo|" {
		t.Errorf("clipboard %q, buffer %q", c.s, show(ed))
	}
	c.s = "from elsewhere"
	st.Key("<D-v>")
	if show(ed) != "one twotwofrom elsewhere|" {
		t.Errorf("pasting another app's text: %q", show(ed))
	}
	ed = load("a\nb|\nc")
	ed.SetClipboard(c)
	st = Engine{}.Attach(ed)
	st.Key("<D-x>")
	if c.s != "b\n" || ed.Buf().String() != "a\nc" {
		t.Errorf("cut with no selection takes the line: %q, buffer %q", c.s, ed.Buf().String())
	}
	if r := st.Key("<D-s>"); r.Command != "editor.save" {
		t.Errorf("⌘S = %+v", r)
	}
	if r := st.Key("<F5>"); r.Handled {
		t.Error("F5 handled")
	}
}
