package emacs

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/text"
)

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

// editor.md's emacs sketch: kill-line, set-mark and self-insert on edit's primitives, a C-x prefix, a kill ring.
func TestEmacsKeys(t *testing.T) {
	for _, tt := range []struct{ name, given, keys, want string }{
		{"C-f and C-b move a character", "a|bc", "<C-f><C-f><C-b>", "ab|c"},
		{"C-n and C-p keep the column", "abc|d\nx\nabcd", "<C-n><C-n>", "abcd\nx\nabc|d"},
		{"C-a and C-e", "x\nfo|o", "<C-a>", "x\n|foo"},
		{"M-f and M-b by word", "|foo bar", "<M-f><M-f><M-b>", "foo |bar"},
		{"self-insert", "a|b", "xy", "axy|b"},
		{"C-k kills to the end of the line", "fo|o bar\nbaz", "<C-k>", "fo|\nbaz"},
		{"C-k at the end kills the line break", "foo|\nbaz", "<C-k>", "foo|baz"},
		{"kills in a row join, and C-y yanks them back", "|a\nb", "<C-k><C-k><C-y>", "a\n|b"},
		{"C-space sets the mark, and the region selects", "|foo bar", "<C-Space><M-f>", "[foo] bar"},
		{"C-w kills the region", "|foo bar", "<C-Space><M-f><C-w>", "| bar"},
		{"M-w copies it, and C-y yanks", "|foo bar", "<C-Space><M-f><M-w><C-e><C-y>", "foo barfoo|"},
		{"C-g drops the mark", "|foo", "<C-Space><C-f><C-g>", "f|oo"},
		{"C-d deletes forward", "|ab", "<C-d>", "|b"},
		{"C-/ undoes the typing at once", "|", "abc<C-/>", "|"},
		{"C-x u undoes too", "|", "abc<C-x>u", "|"},
		{"C-x h marks the whole buffer", "a|b", "<C-x>h", "[ab]"},
	} {
		ed := load(tt.given)
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

func TestEmacsPrefix(t *testing.T) {
	st := Engine{}.Attach(load("|"))
	st.Key("<C-x>")
	if st.Pending() != "C-x" {
		t.Errorf("pending = %q", st.Pending())
	}
	if r := st.Key("<C-s>"); r.Command != "editor.save" || st.Pending() != "" {
		t.Errorf("C-x C-s = %+v, pending %q", r, st.Pending())
	}
	if r := st.Key("<F5>"); r.Handled {
		t.Error("F5 handled")
	}
	if m := st.Mode(); m.Label != "EMACS" || m.KeyMode != "emacs" {
		t.Errorf("mode = %+v", m)
	}
}
