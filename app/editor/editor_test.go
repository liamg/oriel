package editor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/editor/emacs"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// A file opens, takes keys, and :w writes it.
func TestEditorOpensEditsAndWrites(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "one\r\ntwo\r\n"}, []string{"a.txt"})
	r.WaitText("two")
	r.TypeKeys("ddGofour<Esc>")
	r.WaitText("four")
	r.TypeKeys(":w<CR>")
	r.waitFile("a.txt", "two\r\nfour\r\n")
	r.waitNotice("vim.written")
}

// The engine's notices are the IDE's. :q on changes is vim.unsaved, an error notice, where nvim gives E37.
func TestEditorQuitWithChangesNotifies(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "one\n"}, []string{"a.txt"})
	r.WaitText("one")
	r.TypeKeys("x:q<CR>")
	n := r.waitNotice("vim.unsaved")
	if n.Severity != core.SevError || n.Source != "vim" {
		t.Errorf("notice = %+v", n)
	}
	r.TypeKeys("u:q<CR>")
	r.WaitFrame("the editor closed", func(s string) bool { return !strings.Contains(s, "NORMAL") })
}

// "yy in the editor puts the line on the system clipboard, and p pastes what another app copied."
func TestEditorClipboard(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "one\ntwo\n"}, []string{"a.txt"})
	r.WaitText("two")
	r.TypeKeys("yy")
	deadline := time.Now().Add(5 * time.Second)
	for r.Win.Clipboard() != "one\n" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r.Win.Clipboard() != "one\n" {
		t.Fatalf("clipboard = %q", r.Win.Clipboard())
	}
	r.Win.SetClipboard("pasted")
	r.TypeKeys("jp:w<CR>")
	r.waitFile("a.txt", "one\ntpastedwo\n")
}

// ":ls, :copen and :messages open the IDE's switcher, results panel and notification history, not text."
func TestEditorIDEViews(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "one\n", "b.txt": "two\n"}, []string{"a.txt", "b.txt"})
	r.WaitText("two")
	r.TypeKeys(":ls<CR>")
	r.WaitFrame("the buffer switcher", func(s string) bool { return strings.Contains(s, "a.txt") && strings.Contains(s, "%") })
	r.TypeKeys("<Esc>")
	r.WaitFrame("the switcher closed", func(s string) bool { return !strings.Contains(s, "PICK") })
	r.TypeKeys(":cexpr 'a.txt:1:oops'<CR>:copen<CR>")
	r.WaitText("oops")
	r.TypeKeys("<Esc>")
	r.WaitFrame("the quickfix picker closed", func(s string) bool { return !strings.Contains(s, "PICK") })
	r.TypeKeys(":messages<CR>")
	r.WaitText("Copy report")
}

// "A change on disk reloads an unchanged buffer and prompts for a changed one."
func TestEditorReloadsFromDisk(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "one\n"}, []string{"a.txt"})
	r.WaitText("one")
	os.WriteFile(filepath.Join(r.dir, "a.txt"), []byte("changed outside\n"), 0o644)
	r.WaitText("changed outside")
	r.TypeKeys("x")
	r.WaitText("hanged outside")
	os.WriteFile(filepath.Join(r.dir, "a.txt"), []byte("again\n"), 0o644)
	n := r.waitNotice("editor.changed-on-disk.a.txt")
	if n.Severity != core.SevQuestion || len(n.Actions) != 2 || n.Actions[0].Command != "editor.reload" {
		t.Errorf("notice = %+v", n)
	}
}

// :split and CTRL-W commands and a drag in the layout change the same tree.
func TestEditorWindowsAreLayoutPanes(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "alpha\n"}, []string{"a.txt"},
		registry.Keymap{Mode: "global", Keys: "<C-p>", Command: "layout.move", Args: registry.Args{"place": "split", "side": "right"}})
	r.WaitText("alpha")
	r.TypeKeys(":split<CR>")
	r.WaitFrame("two panes", func(s string) bool { return strings.Count(s, "alpha") == 2 })
	shown := r.shown()
	if len(shown) != 2 || shown[0].ID != kit.EditorView || shown[1].ID != kit.EditorView {
		t.Fatalf("layout shows %v", shown)
	}
	r.TypeKeys("<C-w>jx")
	r.WaitFrame("the lower window edited", func(s string) bool { return strings.Count(s, "lpha") == 2 && !strings.Contains(s, "alpha") })
	r.TypeKeys(":vsplit<CR>")
	r.WaitFrame("three panes", func(s string) bool { return strings.Count(s, "lpha") == 3 })
	if n := len(r.shown()); n != 3 {
		t.Fatalf("after :vsplit the layout shows %d panes", n)
	}
	r.TypeKeys(":close<CR>")
	r.WaitFrame("two panes again", func(s string) bool { return strings.Count(s, "lpha") == 2 })
	if n := len(r.shown()); n != 2 {
		t.Fatalf("after :close the layout shows %d panes", n)
	}
}

// An engine swap at runtime per buffer; a sample emacs plugin passes its tests.
func TestEditorInputEnginePerBuffer(t *testing.T) {
	r := runEditorWith(t, map[string]string{"a.txt": "alpha\n", "b.txt": "bravo\n"}, []string{"a.txt", "b.txt"},
		[]plugin.Plugin{&emacs.Plugin{}},
		registry.Keymap{Mode: "global", Keys: "<F2>", Command: "editor.input", Args: registry.Args{"engine": "plain"}},
		registry.Keymap{Mode: "global", Keys: "<F3>", Command: "editor.input", Args: registry.Args{"engine": "emacs"}},
		registry.Keymap{Mode: "global", Keys: "<F4>", Command: "editor.input", Args: registry.Args{"engine": "vim"}},
		registry.Keymap{Mode: "global", Keys: "<F5>", Command: "editor.open", Args: registry.Args{"path": "a.txt"}})
	r.WaitText("bravo")
	r.TypeKeys("<F2>")
	r.WaitText("EDIT")
	r.TypeKeys("<D-a>plain<D-s>")
	r.waitFile("b.txt", "plain\n")
	r.TypeKeys("<F5>")
	r.WaitText("alpha")
	r.WaitText("NORMAL")
	r.TypeKeys("x:w<CR>")
	r.waitFile("a.txt", "lpha\n")
	r.TypeKeys("<F3>")
	r.WaitText("EMACS")
	r.TypeKeys("<C-k>gone<C-x><C-s>")
	r.waitFile("a.txt", "gone\n")
	r.TypeKeys("<F4>")
	r.WaitText("NORMAL")
}
