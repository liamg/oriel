package lang_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui/layout"
)

// :term opens a shell docked at the bottom; typing goes to it and its output is drawn from the VT's grid.
func TestTerminalRunsAShell(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh"}, "env": []string{"PS1=$ "}})
	w := s.Host().Layout().Find(layout.ViewRef{ID: lang.TerminalView, Instance: "1"})
	if w == nil || w.Placement != layout.Docked || w.Edge != layout.Bottom {
		t.Fatalf("the terminal is at %+v", w)
	}
	waitTerm(t, s, "$")
	if m := s.KeyModes()[0]; m != lang.TerminalKeyMode {
		t.Fatalf("keys go to %q", m)
	}
	typeTerm(s, "echo hel''lo")
	s.Press("<CR>")
	waitTerm(t, s, "hello")
	wantText(t, s, "the status line", "TERMINAL")
}

// The placements are the layout's: :term! opens in the main area, and a terminal moves like any pane.
func TestTerminalPlacements(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh"}, "bang": true})
	w := s.Host().Layout().Find(layout.ViewRef{ID: lang.TerminalView, Instance: "1"})
	if w == nil || w.Placement != layout.Tab {
		t.Fatalf(":term! is at %+v", w)
	}
	s.Run("layout.move", registry.Args{"place": "float"})
	if w := s.Host().Layout().Find(layout.ViewRef{ID: lang.TerminalView, Instance: "1"}); w == nil || w.Placement != layout.Floating {
		t.Errorf("after ⌃W F: %+v", w)
	}
}

// Terminal-normal mode (⌃\ ⌃N) puts the screen and scrollback under vim's motions and yank; i goes back.
func TestTerminalNormalMode(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh", "-c", "printf 'first line\\nsecond line\\n'; exec cat"}})
	waitTerm(t, s, "second line")
	s.Press(`<C-\><C-n>`)
	if m := s.KeyModes()[0]; m != lang.TerminalNormalKeyMode {
		t.Fatalf("after ⌃\\ ⌃N keys go to %q", m)
	}
	wantText(t, s, "the mode", "TERM-NORMAL")
	typeTerm(s, "ggyy")
	if got, _ := s.Desktop.ReadClipboard(); got != "first line\n" {
		t.Errorf("yy yanked %q", got)
	}
	typeTerm(s, "i")
	if m := s.KeyModes()[0]; m != lang.TerminalKeyMode {
		t.Errorf("i went back to %q", m)
	}
}

// Shell integration: each command gets a dot for how it exited, [[ and ]] jump between prompts, the last
// command's output can be copied, and the tab follows the shell's directory.
func TestShellIntegration(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh", "-c", rig.ShellMarks}})
	waitTerm(t, s, "still oops")
	x := termsOf(s)
	cmds := x.Commands("1")
	if len(cmds) < 2 || !cmds[0].HasExit || cmds[0].ExitCode != 0 || cmds[1].ExitCode != 1 {
		t.Fatalf("commands %+v", cmds)
	}
	dots := x.ExitDots("1")
	if dots[cmds[0].Prompt.Y] != "ok" || dots[cmds[1].Prompt.Y] != "failed" {
		t.Errorf("exit dots %v", dots)
	}
	s.Run("term.copy_last_output", nil)
	if got, _ := s.Desktop.ReadClipboard(); got != "oops\nstill oops" {
		t.Errorf("the last output copied is %q", got)
	}
	if !strings.Contains(x.Title("1"), "proj") {
		t.Errorf("the tab is %q, not the shell's directory", x.Title("1"))
	}
	s.Press("<C-Esc>")
	typeTerm(s, "G") // the last prompt is the last line
	for _, step := range []struct {
		key  string
		want int
	}{{"[", cmds[1].Prompt.Y}, {"[", cmds[0].Prompt.Y}, {"]", cmds[1].Prompt.Y}, {"]", cmds[2].Prompt.Y}} {
		s.Press(step.key)
		s.Press(step.key)
		if got := x.NormalLine("1"); got != step.want {
			t.Errorf("%s%s went to row %d, want %d", step.key, step.key, got, step.want)
		}
	}
}

// Snapshot on reconnect: the terminals' screens come back when the app starts again on the same directory.
func TestTerminalSnapshotOnReopen(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh", "-c", "printf 'kept across restarts\\n'; exec cat"}})
	waitTerm(t, s, "kept across restarts")
	x := termsOf(s)
	if err := x.Deactivate(t.Context()); err != nil {
		t.Fatal(err)
	}
	h := s.HostAs("builtin:terminal")
	if err := x.Activate(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	s.Run("term.open", registry.Args{"cmd": []string{"sh", "-c", "exec cat"}, "instance": "1"})
	s.Frame()
	s.Frame()
	waitTerm(t, s, "kept across restarts")
}

// In a terminal, ⌃ keys are the program's: the window's ⌃W and ⌃J chords don't take them, though ⌘ chords still
// work; ⌃Esc and ⌃\ ⌃N, keymaps like any other, leave for terminal-normal mode.
func TestTerminalKeepsControlKeys(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh", "-c", "stty raw -echo; echo ready; dd bs=1 count=1 2>/dev/null | od -An -c; exec cat"}})
	waitTerm(t, s, "ready") // a key before the tty is raw is the line discipline's
	for _, k := range []string{"<C-w>", "<C-j>", "<C-a>"} {
		if slices.Contains(s.KeyModesFor(k), "global") {
			t.Errorf("%s reaches the global maps in a terminal: %v", k, s.KeyModesFor(k))
		}
	}
	if !slices.Contains(s.KeyModesFor("<D-k>"), "global") {
		t.Error("⌘K doesn't reach the global maps from a terminal")
	}
	s.Press("<C-w>")
	waitTerm(t, s, "027") // od's octal for ⌃W, which reached the program
	for _, k := range []string{"<C-Esc>", `<C-\><C-n>`} {
		m, _ := s.Registry().Lookup([]string{lang.TerminalKeyMode}, keymap.Parse(k))
		if m == nil || m.Command != "term.normal" {
			t.Errorf("%s in a terminal is %+v", k, m)
		}
	}
}

// A click outside a terminal takes the keys from it, and one on it gives them back.
func TestTerminalClickMovesKeys(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.Frame()
	s.Run("editor.open", registry.Args{"path": "a.txt"})
	s.Frame()
	s.Run("term.open", registry.Args{"cmd": []string{"sh", "-c", "exec cat"}})
	s.Frame()
	s.Frame()
	if m := s.KeyModes()[0]; m != lang.TerminalKeyMode {
		t.Fatalf("an opened terminal doesn't take the keys: %q", m)
	}
	s.Click(render.Point{X: s.Size().X / 2, Y: s.Size().Y / 4}, 0)
	s.Frame()
	if m := s.KeyModes()[0]; m == lang.TerminalKeyMode {
		t.Fatalf("after a click in the main area keys still go to %q", m)
	}
	if slices.Contains(s.KeyModesFor("<C-w>"), lang.TerminalKeyMode) || !slices.Contains(s.KeyModesFor("<C-w>"), "global") {
		t.Errorf("⌃W outside the terminal goes to %v", s.KeyModesFor("<C-w>"))
	}
	s.Click(render.Point{X: s.Size().X / 2, Y: s.Size().Y - s.Size().Y/8}, 0)
	s.Frame()
	if m := s.KeyModes()[0]; m != lang.TerminalKeyMode {
		t.Errorf("after a click on the terminal keys go to %q", m)
	}
}

// Terminal-normal mode keeps the program's colours, and the scrollback above the screen keeps them too.
func TestTerminalNormalKeepsColours(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh", "-c", "printf '\\033[31mred line\\033[0m\\n'; for i in $(seq 1 40); do echo filler $i; done; exec cat"}})
	waitTerm(t, s, "filler 40")
	s.Press("<C-Esc>")
	typeTerm(s, "gg")
	s.Frame()
	plain := s.UI().Theme().C(kit.TTermFg)
	for _, op := range s.List().Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == "red line" {
			if op.Color == plain {
				t.Error("the red line is drawn in the plain colour")
			}
			return
		}
	}
	t.Fatalf("no red line in terminal-normal mode:\n%s", s.Text())
}

// Text in a terminal selects with the mouse, a drag from the press or a double click on a word, and ⌘C copies it,
// as in any terminal on the platform. With nothing selected, ⌘C goes to the program.
func TestTerminalMouseSelectsAndCopies(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh", "-c", "printf 'first line\\nsecond line\\n'; exec cat"}})
	waitTerm(t, s, "second line")
	s.Frame()
	s.Drag(charAt(t, s, "first line", 0), charAt(t, s, "second line", 5))
	s.Press("<D-c>")
	if got, _ := s.Desktop.ReadClipboard(); got != "first line\nsecond" {
		t.Errorf("a drag and ⌘C copied %q", got)
	}
	word := charAt(t, s, "second line", 8)
	s.Click(word, 0)
	s.Click(word, 0)
	s.Press("<C-S-c>")
	if got, _ := s.Desktop.ReadClipboard(); got != "line" {
		t.Errorf("a double click and ⌃⇧C copied %q", got)
	}
	if termsOf(s).Selected("1") {
		t.Error("the selection stayed after it was copied")
	}
	s.Press("<D-a>")
	s.Press("<D-c>")
	if got, _ := s.Desktop.ReadClipboard(); !strings.HasPrefix(got, "first line\nsecond line") {
		t.Errorf("⌘A and ⌘C copied %q", got)
	}
}

// charAt is the middle of the col'th character of the run of text reading line.
func charAt(t *testing.T, s *apptest.Scene, line string, col int) render.Point {
	t.Helper()
	for _, op := range s.List().Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == line {
			w := op.Run.Advance / float32(len(line))
			return render.Point{X: op.At.X + (float32(col)+.5)*w, Y: op.At.Y - op.Run.Ascent/2}
		}
	}
	t.Fatalf("no line %q in the frame:\n%s", line, s.Text())
	return render.Point{}
}

// Right-click a terminal's tab, Rename…, type, ⏎: the tab has the name; an empty name puts back the program's.
func TestTerminalRenames(t *testing.T) {
	s := termScene(t, registry.Args{"cmd": []string{"sh"}, "env": []string{"PS1=$ "}, "bang": true})
	waitTerm(t, s, "$")
	s.Until("the tab", func() bool { return s.Has("sh") })
	s.RightClick(s.TextPoint("sh"))
	s.Until("the menu", func() bool { return s.Has("Rename…") })
	s.ClickText("Rename…")
	s.Until("the name field", func() bool { return s.Find("Terminal name") != nil })
	s.Type("d", "b", " ", "s", "h", "e", "l", "l")
	s.Press("<CR>")
	s.Until("the tab named", func() bool { return s.Has("db shell") && s.Find("Terminal name") == nil })
	s.Run("term.rename", registry.Args{"instance": "1", "name": ""})
	s.Until("the program's name back", func() bool { return !s.Has("db shell") })
}
