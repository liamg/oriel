package vim_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/vim"
)

// Multi-cursor (ED-8) is Oriel's own, after vim-visual-multi: nvim has none, so there's no oracle for these.

func multiEngine(t *testing.T, body string, line, col int) *vim.Engine {
	t.Helper()
	ed := edit.New(text.New([]byte(body), text.Author{}), text.Author{Kind: text.User})
	e := vim.New(ed)
	e.SetCursor(vim.Pos{Line: line, Col: col})
	return e
}

func keys(e *vim.Engine, ks string) {
	for _, k := range keymap.Parse(ks) {
		e.Key(k)
	}
}

func bufText(e *vim.Engine) string { return string(e.Buf().Bytes()) }

func regions(e *vim.Engine) string {
	m, ok := e.Multi()
	if !ok {
		return "(none)"
	}
	var out []string
	for i, r := range m.Regions {
		s := ""
		if i == m.Primary {
			s = "*"
		}
		if m.Extend {
			s += fmtPos(r.Anchor) + "-" + fmtPos(r.Head)
		} else {
			s += fmtPos(r.Head)
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

func fmtPos(p vim.Pos) string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

// ⌃n selects the word under the cursor, then each next whole-word match; c changes every one, typing goes to all of
// them, and the change and the typing undo as one step.
func TestMultiNextChangeUndo(t *testing.T) {
	e := multiEngine(t, "foo bar foo baz food foo", 0, 1)
	e.MultiNext()
	e.MultiNext()
	e.MultiNext()
	if got, want := regions(e), "0:0-0:2 0:8-0:10 *0:21-0:23"; got != want {
		t.Fatalf("after three ⌃n: %s, want %s (food isn't a whole-word match)", got, want)
	}
	keys(e, "cxyz<Esc>")
	if got := bufText(e); got != "xyz bar xyz baz food xyz" {
		t.Fatalf("after cxyz<Esc>: %q", got)
	}
	if e.Mode() != "normal" {
		t.Errorf("mode %s", e.Mode())
	}
	keys(e, "u")
	if got := bufText(e); got != "foo bar foo baz food foo" {
		t.Errorf("u gave %q: the change didn't undo as one", got)
	}
	if _, ok := e.Multi(); ok {
		t.Error("u left the cursors")
	}
}

// ⌃↓ adds a cursor on the next line at the same column; a normal-mode command runs at every cursor, as one undo step.
func TestMultiVerticalCommands(t *testing.T) {
	e := multiEngine(t, "a1 one\na2 two\na3 three", 0, 1)
	e.MultiVertical(1)
	e.MultiVertical(1)
	if got := regions(e); got != "0:1 1:1 *2:1" {
		t.Fatalf("cursors %s", got)
	}
	keys(e, "x")
	if got := bufText(e); got != "a one\na two\na three" {
		t.Fatalf("x at every cursor: %q", got)
	}
	keys(e, "wdw")
	if got := bufText(e); got != "a \na \na " {
		t.Fatalf("wdw at every cursor: %q", got)
	}
	keys(e, "A!<Esc>")
	if got := bufText(e); got != "a !\na !\na !" {
		t.Fatalf("A! at every cursor: %q", got)
	}
	keys(e, "<Esc>")
	if _, ok := e.Multi(); ok {
		t.Fatal("Esc in normal mode didn't end it")
	}
	keys(e, "u")
	if got := bufText(e); got != "a \na \na " {
		t.Errorf("u took back more than the last command: %q", got)
	}
}

// q skips the region just added for the next match; Q drops it.
func TestMultiSkipAndRemove(t *testing.T) {
	e := multiEngine(t, "foo foo foo foo", 0, 0)
	e.MultiNext()
	e.MultiNext()
	keys(e, "q")
	if got := regions(e); got != "0:0-0:2 *0:8-0:10" {
		t.Fatalf("after q: %s", got)
	}
	keys(e, "Q")
	if got := regions(e); got != "*0:0-0:2" {
		t.Fatalf("after Q: %s", got)
	}
	e2 := multiEngine(t, "foo foo foo foo", 0, 0)
	e2.MultiNext()
	e2.MultiNext()
	keys(e2, "d")
	if got := bufText(e2); got != "  foo foo" {
		t.Errorf("d in extend mode deletes every region: %q", got)
	}
}

// In insert mode typing, ⌫ and ⏎ go to every cursor.
func TestMultiInsert(t *testing.T) {
	e := multiEngine(t, "ab\ncd", 0, 1)
	e.MultiVertical(1)
	keys(e, "iXY<BS>Z<CR>-<Esc>")
	if got := bufText(e); got != "aXZ\n-b\ncXZ\n-d" {
		t.Fatalf("typing at two cursors: %q", got)
	}
	keys(e, "<Esc>u")
	if got := bufText(e); got != "ab\ncd" {
		t.Errorf("the insert didn't undo as one: %q", got)
	}
}

// ⇥ switches between cursors and the regions around them; I and A put the cursors at the regions' ends to insert.
func TestMultiExtendInsert(t *testing.T) {
	e := multiEngine(t, "one two one", 0, 0)
	e.MultiNext()
	e.MultiNext()
	keys(e, "A!<Esc>")
	if got := bufText(e); got != "one! two one!" {
		t.Fatalf("A in extend mode: %q", got)
	}
	keys(e, "<Esc>")
	e.SetCursor(vim.Pos{Line: 0, Col: 5})
	e.MultiNext()
	keys(e, "<Tab>")
	if m, ok := e.Multi(); !ok || m.Extend {
		t.Errorf("⇥ didn't switch to cursors: %+v", m)
	}
}
