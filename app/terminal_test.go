package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui/layout"
)

func (s *errScene) terms() *terminals {
	for _, e := range s.plugins {
		if x, ok := e.(*terminals); ok {
			return x
		}
	}
	return nil
}

// waitTerm frames until the frame has want, as the shell answers off the loop.
func (s *errScene) waitTerm(t *testing.T, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.sh.runTodo()
		s.frame()
		text := s.frameText()
		if strings.Contains(text, want) {
			return text
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q in:\n%s", want, text)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// typeTerm types text at the focused terminal, a key at a time, as the window would.
func (s *errScene) typeTerm(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		for _, r := range k {
			s.keyCommand(t, string(r))
		}
	}
}

func termScene(t *testing.T, args registry.Args) *errScene {
	s := newScene(t, "", t.TempDir(), mockupSize)
	s.frame()
	s.run("term.open", args)
	s.frame()
	s.frame()
	return s
}

// FG-4: :term opens a shell docked at the bottom; typing goes to it and its output is drawn from the VT's grid.
func TestTerminalRunsAShell(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh"}, "env": []string{"PS1=$ "}})
	w := s.sh.layout.root.Find(layout.ViewRef{ID: TerminalView, Instance: "1"})
	if w == nil || w.Placement != layout.Docked || w.Edge != layout.Bottom {
		t.Fatalf("the terminal is at %+v", w)
	}
	s.waitTerm(t, "$")
	if m := s.sh.keyModes()[0]; m != TerminalKeyMode {
		t.Fatalf("keys go to %q", m)
	}
	s.typeTerm(t, "echo hel''lo")
	s.keyCommand(t, "<CR>")
	s.waitTerm(t, "hello")
	s.wantText(t, "the status line", "TERMINAL")
}

// FG-4: the placements are the layout's: :term! opens in the main area, and a terminal moves like any pane.
func TestTerminalPlacements(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh"}, "bang": true})
	w := s.sh.layout.root.Find(layout.ViewRef{ID: TerminalView, Instance: "1"})
	if w == nil || w.Placement != layout.Tab {
		t.Fatalf(":term! is at %+v", w)
	}
	s.run("layout.move", registry.Args{"place": "float"})
	if w := s.sh.layout.root.Find(layout.ViewRef{ID: TerminalView, Instance: "1"}); w == nil || w.Placement != layout.Floating {
		t.Errorf("after ⌃W F: %+v", w)
	}
}

// FG-4: terminal-normal mode (⌃\ ⌃N) puts the screen and scrollback under vim's motions and yank; i goes back.
func TestTerminalNormalMode(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh", "-c", "printf 'first line\\nsecond line\\n'; exec cat"}})
	s.waitTerm(t, "second line")
	s.keyCommand(t, `<C-\><C-n>`)
	if m := s.sh.keyModes()[0]; m != TerminalNormalKeyMode {
		t.Fatalf("after ⌃\\ ⌃N keys go to %q", m)
	}
	s.wantText(t, "the mode", "TERM-NORMAL")
	s.typeTerm(t, "ggyy")
	if got, _ := s.clip.ReadClipboard(); got != "first line\n" {
		t.Errorf("yy yanked %q", got)
	}
	s.typeTerm(t, "i")
	if m := s.sh.keyModes()[0]; m != TerminalKeyMode {
		t.Errorf("i went back to %q", m)
	}
}

// shellMarks is a program that prints two commands as a shell with OSC 133 and 7 would: one that exits 0, one 1.
const shellMarks = `printf '\033]7;file://host/tmp/proj\a'
printf '\033]133;A\a$ \033]133;B\aecho ok\n\033]133;C\aok\n\033]133;D;0\a'
printf '\033]133;A\a$ \033]133;B\afalse\n\033]133;C\aoops\nstill oops\n\033]133;D;1\a'
printf '\033]133;A\a$ \033]133;B\a'
exec cat`

// FG-5: shell integration: each command gets a dot for how it exited, [[ and ]] jump between prompts, the last
// command's output can be copied, and the tab follows the shell's directory.
func TestShellIntegration(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh", "-c", shellMarks}})
	s.waitTerm(t, "still oops")
	x := s.terms()
	tm := x.get("1")
	cmds := tm.emu.Commands()
	if len(cmds) < 2 || !cmds[0].HasExit || cmds[0].ExitCode != 0 || cmds[1].ExitCode != 1 {
		t.Fatalf("commands %+v", cmds)
	}
	dots := tm.exitDots()
	if dots[cmds[0].Prompt.Y] != "ok" || dots[cmds[1].Prompt.Y] != "failed" {
		t.Errorf("exit dots %v", dots)
	}
	s.run("term.copy_last_output", nil)
	if got, _ := s.clip.ReadClipboard(); got != "oops\nstill oops" {
		t.Errorf("the last output copied is %q", got)
	}
	if !strings.Contains(tm.title(), "proj") {
		t.Errorf("the tab is %q, not the shell's directory", tm.title())
	}
	s.keyCommand(t, "<C-Esc>")
	s.typeTerm(t, "G") // the last prompt is the last line
	for _, step := range []struct {
		key  string
		want int
	}{{"[", cmds[1].Prompt.Y}, {"[", cmds[0].Prompt.Y}, {"]", cmds[1].Prompt.Y}, {"]", cmds[2].Prompt.Y}} {
		s.keyCommand(t, step.key)
		s.keyCommand(t, step.key)
		if got := x.normalLine(tm); got != step.want {
			t.Errorf("%s%s went to row %d, want %d", step.key, step.key, got, step.want)
		}
	}
}

// FG-4: "snapshot on reconnect": the terminals' screens come back when the app starts again on the same directory.
func TestTerminalSnapshotOnReopen(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh", "-c", "printf 'kept across restarts\\n'; exec cat"}})
	s.waitTerm(t, "kept across restarts")
	x := s.terms()
	if err := x.Deactivate(t.Context()); err != nil {
		t.Fatal(err)
	}
	h := s.sh.host("builtin:terminal")
	if err := x.Activate(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	s.run("term.open", registry.Args{"cmd": []string{"sh", "-c", "exec cat"}, "instance": "1"})
	s.frame()
	s.frame()
	s.waitTerm(t, "kept across restarts")
}

// In a terminal, ⌃ keys are the program's: the window's ⌃W and ⌃J chords don't take them, though ⌘ chords still
// work; ⌃Esc and ⌃\ ⌃N, keymaps like any other, leave for terminal-normal mode.
func TestTerminalKeepsControlKeys(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh", "-c", "stty raw -echo; dd bs=1 count=1 2>/dev/null | od -An -c; exec cat"}})
	s.frame()
	for _, k := range []string{"<C-w>", "<C-j>", "<C-a>"} {
		if slices.Contains(s.sh.keyModesFor(k), "global") {
			t.Errorf("%s reaches the global maps in a terminal: %v", k, s.sh.keyModesFor(k))
		}
	}
	if !slices.Contains(s.sh.keyModesFor("<D-k>"), "global") {
		t.Error("⌘K doesn't reach the global maps from a terminal")
	}
	s.keyCommand(t, "<C-w>")
	s.waitTerm(t, "027") // od's octal for ⌃W, which reached the program
	for _, k := range []string{"<C-Esc>", `<C-\><C-n>`} {
		m, _ := s.reg.Lookup([]string{TerminalKeyMode}, keymap.Parse(k))
		if m == nil || m.Command != "term.normal" {
			t.Errorf("%s in a terminal is %+v", k, m)
		}
	}
}

// A click outside a terminal takes the keys from it, and one on it gives them back.
func TestTerminalClickMovesKeys(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644)
	s := newScene(t, "", dir, mockupSize)
	s.frame()
	s.run("editor.open", registry.Args{"path": "a.txt"})
	s.frame()
	s.run("term.open", registry.Args{"cmd": []string{"sh", "-c", "exec cat"}})
	s.frame()
	s.frame()
	if m := s.sh.keyModes()[0]; m != TerminalKeyMode {
		t.Fatalf("an opened terminal doesn't take the keys: %q", m)
	}
	s.click(render.Point{X: s.size.X / 2, Y: s.size.Y / 4})
	s.frame()
	if m := s.sh.keyModes()[0]; m == TerminalKeyMode {
		t.Fatalf("after a click in the main area keys still go to %q", m)
	}
	if slices.Contains(s.sh.keyModesFor("<C-w>"), TerminalKeyMode) || !slices.Contains(s.sh.keyModesFor("<C-w>"), "global") {
		t.Errorf("⌃W outside the terminal goes to %v", s.sh.keyModesFor("<C-w>"))
	}
	s.click(render.Point{X: s.size.X / 2, Y: s.size.Y - s.size.Y/8})
	s.frame()
	if m := s.sh.keyModes()[0]; m != TerminalKeyMode {
		t.Errorf("after a click on the terminal keys go to %q", m)
	}
}

// FG-4: terminal-normal mode keeps the program's colours, and the scrollback above the screen keeps them too.
func TestTerminalNormalKeepsColours(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh", "-c", "printf '\\033[31mred line\\033[0m\\n'; for i in $(seq 1 40); do echo filler $i; done; exec cat"}})
	s.waitTerm(t, "filler 40")
	s.keyCommand(t, "<C-Esc>")
	s.typeTerm(t, "gg")
	s.frame()
	plain := s.ui.Theme().C(tTermFg)
	for _, op := range s.list.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == "red line" {
			if op.Color == plain {
				t.Error("the red line is drawn in the plain colour")
			}
			return
		}
	}
	t.Fatalf("no red line in terminal-normal mode:\n%s", s.frameText())
}

// Text in a terminal selects with the mouse, a drag from the press or a double click on a word, and ⌘C copies it,
// as in any terminal on the platform. With nothing selected, ⌘C goes to the program.
func TestTerminalMouseSelectsAndCopies(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh", "-c", "printf 'first line\\nsecond line\\n'; exec cat"}})
	s.waitTerm(t, "second line")
	s.frame()
	s.drag(s.charAt(t, "first line", 0), s.charAt(t, "second line", 5))
	s.keyCommand(t, "<D-c>")
	if got, _ := s.clip.ReadClipboard(); got != "first line\nsecond" {
		t.Errorf("a drag and ⌘C copied %q", got)
	}
	word := s.charAt(t, "second line", 8)
	s.click(word)
	s.click(word)
	s.keyCommand(t, "<C-S-c>")
	if got, _ := s.clip.ReadClipboard(); got != "line" {
		t.Errorf("a double click and ⌃⇧C copied %q", got)
	}
	if _, _, ok := s.terms().get("1").selection(); ok {
		t.Error("the selection stayed after it was copied")
	}
	s.keyCommand(t, "<D-a>")
	s.keyCommand(t, "<D-c>")
	if got, _ := s.clip.ReadClipboard(); !strings.HasPrefix(got, "first line\nsecond line") {
		t.Errorf("⌘A and ⌘C copied %q", got)
	}
}
