package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/vim"
)

func navScene(t *testing.T, dir string) *errScene {
	t.Helper()
	s := newScene(t, "", dir, render.Point{X: 1200, Y: 800})
	for _, id := range []core.SessionID{"pricing", "billing"} {
		if err := s.store.Apply(core.SessionSynced{Session: core.SessionState{ID: id, Name: string(id), Kind: core.KindWork, Status: core.StatusQuiet}}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func (s *errScene) at(t *testing.T, what string, session core.SessionID, place string) {
	t.Helper()
	s.RunQueued()
	s.frame()
	got, _ := registry.ShownPlace(s.reg)
	if f := s.store.Focus(); f != session || got != place {
		t.Errorf("%s: on %q with %q showing, want %q with %q", what, f, got, session, place)
	}
}

// The side buttons go back and forward through the sessions and pages the window has shown, like a browser's.
func TestSideButtonsGoBackThroughSessionsAndPages(t *testing.T) {
	s := navScene(t, "")
	s.store.Apply(core.SessionFocused{ID: "pricing"})
	s.frame()
	s.store.Apply(core.SessionFocused{ID: "billing"})
	s.frame()
	s.run("settings.open", nil)
	s.at(t, "settings opened", "billing", "settings")

	s.keyCommand(t, "<X1Mouse>")
	s.at(t, "back once", "billing", "")
	s.keyCommand(t, "<X1Mouse>")
	s.at(t, "back twice", "pricing", "")
	s.keyCommand(t, "<X1Mouse>")
	s.at(t, "back at the start", "pricing", "")
	s.keyCommand(t, "<X2Mouse>")
	s.keyCommand(t, "<X2Mouse>")
	s.at(t, "forward twice", "billing", "settings")

	// Somewhere new after going back drops what was forward, and a session that's gone is passed over.
	s.keyCommand(t, "<X1Mouse>")
	s.keyCommand(t, "<X1Mouse>")
	s.run("stats.open", nil)
	s.at(t, "stats opened", "pricing", "stats")
	s.keyCommand(t, "<X2Mouse>")
	s.at(t, "nothing forward", "pricing", "stats")
	s.store.Apply(core.SessionFocused{ID: "billing"})
	s.at(t, "billing over stats", "billing", "stats")
	s.keyCommand(t, "<Esc>")
	s.at(t, "stats closed", "billing", "")
	s.store.Apply(core.SessionArchived{ID: "billing", Archived: true})
	s.keyCommand(t, "<X1Mouse>")
	s.at(t, "past the archived session", "pricing", "stats")
}

// In the editor, back goes through the jumplist first, as CTRL-O does, then through the window's history.
func TestSideButtonsInTheEditorJumpFirst(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\nfour\n"), 0o644)
	s := navScene(t, dir)
	s.store.Apply(core.SessionFocused{ID: "pricing"})
	s.frame()
	s.store.Apply(core.SessionFocused{ID: "billing"})
	s.open(t, "a.txt")
	s.frame()
	e := s.editor().e
	e.TypeMapped([]string{"G"}, true)
	s.keyCommand(t, "<X1Mouse>")
	if c := e.Cursor(); c != (vim.Pos{}) {
		t.Errorf("back in the editor went to %+v, not the jump before", c)
	}
	s.at(t, "back through the jumplist", "billing", "")
	s.keyCommand(t, "<X1Mouse>")
	s.at(t, "back past the jumplist", "pricing", "")
}

// Back to a session in another directory opens the window there again, every plugin starting over, and then the page
// it showed.
func TestSideButtonsBackToAnotherDirectory(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	s := newScene(t, "", a, render.Point{X: 1200, Y: 800})
	for id, dir := range map[core.SessionID]string{"pricing": a, "billing": b} {
		s.store.Apply(core.SessionSynced{Session: core.SessionState{ID: id, Name: string(id), Kind: core.KindWork, Status: core.StatusQuiet, Dir: dir}})
	}
	settle := func() {
		for range 3 {
			s.RunQueued()
			s.reopen()
		}
	}
	s.store.Apply(core.SessionFocused{ID: "pricing"})
	settle()
	s.run("stats.open", nil)
	s.at(t, "stats in pricing", "pricing", "stats")
	s.store.Apply(core.SessionFocused{ID: "billing"})
	settle()
	if s.sh.cfg.WorkDir != b {
		t.Fatalf("the window didn't open again on billing's directory: %s", s.sh.cfg.WorkDir)
	}
	s.at(t, "billing", "billing", "")
	s.keyCommand(t, "<X1Mouse>")
	settle()
	if s.sh.cfg.WorkDir != a {
		t.Errorf("back didn't open the window on pricing's directory: %s", s.sh.cfg.WorkDir)
	}
	s.at(t, "back to pricing", "pricing", "stats")
}

// A Lua plugin's page joins the history as a place: back closes it, forward opens it again with its command.
func TestLuaPageIsAPlace(t *testing.T) {
	s := luaScene(t, `
local open = false
oriel.command("Board", function() open = true end, { desc = "the board" })
oriel.place.add{ name = "board", title = "Board", command = "Board", shown = function() return open end, close = function() open = false end }
`)
	s.frame()
	s.run("Board", nil)
	s.at(t, "the board opened", "", "board")
	s.keyCommand(t, "<X1Mouse>")
	s.at(t, "back", "", "")
	s.keyCommand(t, "<X2Mouse>")
	s.at(t, "forward", "", "board")
}

// A page gives way to what's put in front: a view shown from the sidebar or a command, or a file opened, closes the
// page over the layout. A view already open under the page is revealed, not toggled away.
func TestPagesGiveWayToViews(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	s := navScene(t, dir)
	shown := func() string { p, _ := registry.ShownPlace(s.reg); return p }
	explorer := registry.Args{"id": ExplorerView, "place": "dock", "edge": "left", "title": "Explorer"}

	s.run("settings.open", nil)
	s.frame()
	s.run("view.toggle", explorer)
	s.frame()
	if p := shown(); p != "" {
		t.Errorf("%s stayed over the Explorer", p)
	}
	if !showing(s.host, ExplorerView, "") {
		t.Error("the Explorer didn't open")
	}

	s.run("stats.open", nil)
	s.frame()
	s.run("view.toggle", explorer)
	s.frame()
	if p := shown(); p != "" || !showing(s.host, ExplorerView, "") {
		t.Errorf("the Explorer open under %q was toggled away, or the page stayed", p)
	}

	s.run("settings.open", nil)
	s.frame()
	s.open(t, "a.txt")
	s.frame()
	if p := shown(); p != "" {
		t.Errorf("%s stayed over the file opened", p)
	}
}
