package app

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/layout"
)

func (s *errScene) terms() *terminals {
	for _, e := range s.exts {
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
	s.keyCommand(t, "<C-\\>")
	s.keyCommand(t, "<C-n>")
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
	s.keyCommand(t, "<C-\\>")
	s.keyCommand(t, "<C-n>")
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
