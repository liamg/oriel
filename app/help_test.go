package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

func (s *errScene) help() *helpView {
	for _, e := range s.exts {
		if h, ok := e.(*helpView); ok {
			return h
		}
	}
	return nil
}

// lint is help.md's: every interactive node in the frame has a help id, and every help id resolves to a page and
// anchor.
func (s *errScene) lint(state string) {
	s.t.Helper()
	if missing := s.ui.WithoutHelp(); len(missing) > 0 {
		s.t.Errorf("%s: %d interactive nodes have no help id", state, len(missing))
	}
	ix := s.help().index()
	for _, n := range s.ui.HelpNodes() {
		if _, _, ok := ix.For(n.Help); !ok {
			s.t.Errorf("%s: help id %q doesn't resolve to a page and anchor", state, n.Help)
		}
	}
}

// SH-10: "CI lint: every interactive element resolves to a help page and anchor", across the app's surfaces.
func TestHelpLint(t *testing.T) {
	s := omniScene(t, "pri")
	s.lint("omnibox")
	s.omni().open = false
	s.run("picker.open", registry.Args{"source": "files", "query": "go"})
	s.frame()
	s.frame()
	s.lint("picker")
	s.picker().open = false
	for i, sev := range []core.Severity{core.SevError, core.SevWarning, core.SevInfo} {
		s.host.Notify(core.Notice{ID: fmt.Sprint("n", i), Severity: sev, Text: "a notice", Time: s.now,
			Actions: []core.Action{{Title: "Do it", Command: "Log"}}})
	}
	s.run("Log", nil)
	s.frame()
	s.frame()
	s.lint("toasts and the log")
	s.run("help.open", registry.Args{"id": "errors#log"})
	s.frame()
	s.frame()
	s.lint("a help page")
	s.help().open = false
	s.run("help.mode", nil)
	s.frame()
	s.frame()
	s.lint("help mode")
	s.help().mode = false
	s.run("theme.edit", nil)
	s.frame()
	s.frame()
	s.lint("the theme editor")
	s.run("theme.pick", nil)
	s.frame()
	s.frame()
	s.lint("pick mode")
	s.themeEditor().picking = false
	s.run("settings.open", registry.Args{"section": "ui"})
	s.frame()
	s.frame()
	s.lint("the settings page")
}

func TestHelpWays(t *testing.T) {
	s := editorScene(t)
	hv := s.help()
	// :help topic, by words of a title.
	s.editor().e.Run("help pickers")
	s.frame()
	if !hv.open || hv.cur != "shell/pickers" {
		t.Errorf(":help pickers opened %q", hv.cur)
	}
	text := strings.ReplaceAll(s.frameText(), "\n", "")
	if !strings.Contains(text, "A picker searches one source") || !strings.Contains(text, "Resume the last picker") {
		t.Errorf("the page:\n%s", s.frameText())
	}
	// Related pages, then ⌃o back.
	s.run("help.open", registry.Args{"id": "shell/omnibox"})
	s.run("help.back", nil)
	if hv.cur != "shell/pickers" {
		t.Errorf("back went to %q", hv.cur)
	}
	s.run("help.forward", nil)
	if hv.cur != "shell/omnibox" {
		t.Errorf("forward went to %q", hv.cur)
	}
	hv.open = false
	s.frame()
	// F1 on what has focus: the editor pane.
	s.run("help.focused", nil)
	if hv.cur != "editor/pane" {
		t.Errorf("F1 opened %q", hv.cur)
	}
	hv.open = false
	// Right-click anything: Help is its menu's last item.
	s.frame()
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 300, Y: 300}})
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: render.Point{X: 300, Y: 300}, Buttons: render.ButtonSecondary})
	s.frame()
	s.frame()
	if !strings.Contains(s.frameText(), "? Help") {
		t.Fatalf("no Help in the right-click menu:\n%s", s.frameText())
	}
	// Help mode: a ? on everything with help; clicking one opens its page.
	s.run("help.mode", nil)
	s.frame()
	s.frame()
	if n := strings.Count(s.frameText(), "?"); n < 3 {
		t.Errorf("help mode shows %d markers", n)
	}
}

// 28-help: a page, against a golden.
func TestHelpSnapshot(t *testing.T) {
	s := newScene(t, "", t.TempDir(), render.Point{X: 1600, Y: 1000})
	s.run("help.open", registry.Args{"id": "errors#log"})
	s.golden("help-page")
}
