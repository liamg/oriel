package shell_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/text"
)

func navScene(t *testing.T, dir string) *apptest.Scene {
	t.Helper()
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: render.Point{X: 1200, Y: 800}})
	for _, id := range []core.SessionID{"pricing", "billing"} {
		if err := s.Store().Apply(core.SessionSynced{Session: core.SessionState{ID: id, Name: string(id), Kind: core.KindWork, Status: core.StatusQuiet}}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func at(s *apptest.Scene, t *testing.T, what string, session core.SessionID, place string) {
	t.Helper()
	s.RunQueued()
	s.Frame()
	got, _ := uireg.ShownPlace(s.Registry())
	if f := s.Store().Focus(); f != session || got != place {
		t.Errorf("%s: on %q with %q showing, want %q with %q", what, f, got, session, place)
	}
}

// windowDir is the directory the window is open on, as the window service says.
func windowDir(s *apptest.Scene) string {
	w, _ := kit.WindowOf(s.Registry())
	return w.Dir()
}

// The side buttons go back and forward through the sessions and pages the window has shown, like a browser's.
func TestSideButtonsGoBackThroughSessionsAndPages(t *testing.T) {
	s := navScene(t, "")
	s.Store().Apply(core.SessionFocused{ID: "pricing"})
	s.Frame()
	s.Store().Apply(core.SessionFocused{ID: "billing"})
	s.Frame()
	s.Run("settings.open", nil)
	at(s, t, "settings opened", "billing", "settings")

	s.Press("<X1Mouse>")
	at(s, t, "back once", "billing", "")
	s.Press("<X1Mouse>")
	at(s, t, "back twice", "pricing", "")
	s.Press("<X1Mouse>")
	at(s, t, "back at the start", "pricing", "")
	s.Press("<X2Mouse>")
	s.Press("<X2Mouse>")
	at(s, t, "forward twice", "billing", "settings")

	// Somewhere new after going back drops what was forward, and a session that's gone is passed over.
	s.Press("<X1Mouse>")
	s.Press("<X1Mouse>")
	s.Run("stats.open", nil)
	at(s, t, "stats opened", "pricing", "stats")
	s.Press("<X2Mouse>")
	at(s, t, "nothing forward", "pricing", "stats")
	s.Store().Apply(core.SessionFocused{ID: "billing"})
	at(s, t, "billing over stats", "billing", "stats")
	s.Press("<Esc>")
	at(s, t, "stats closed", "billing", "")
	s.Store().Apply(core.SessionArchived{ID: "billing", Archived: true})
	s.Press("<X1Mouse>")
	at(s, t, "past the archived session", "pricing", "stats")
}

// In the editor, back goes through the jumplist first, as CTRL-O does, then through the window's history.
func TestSideButtonsInTheEditorJumpFirst(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\nfour\n"), 0o644)
	s := navScene(t, dir)
	s.Store().Apply(core.SessionFocused{ID: "pricing"})
	s.Frame()
	s.Store().Apply(core.SessionFocused{ID: "billing"})
	s.Run("editor.open", registry.Args{"path": "a.txt"})
	s.Frame()
	s.Run("editor.keys", registry.Args{"keys": "G", "noremap": true})
	s.Press("<X1Mouse>")
	if c := s.EditorWindow().Cursor; c != (text.Pos{}) {
		t.Errorf("back in the editor went to %+v, not the jump before", c)
	}
	at(s, t, "back through the jumplist", "billing", "")
	s.Press("<X1Mouse>")
	at(s, t, "back past the jumplist", "pricing", "")
}

// Back to a session in another directory opens the window there again, every plugin starting over, and then the page
// it showed.
func TestSideButtonsBackToAnotherDirectory(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	s := apptest.New(t, apptest.Options{WorkDir: a, Size: render.Point{X: 1200, Y: 800}})
	for id, dir := range map[core.SessionID]string{"pricing": a, "billing": b} {
		s.Store().Apply(core.SessionSynced{Session: core.SessionState{ID: id, Name: string(id), Kind: core.KindWork, Status: core.StatusQuiet, Dir: dir}})
	}
	settle := func() {
		for range 3 {
			s.RunQueued()
			s.Reopen()
		}
	}
	s.Store().Apply(core.SessionFocused{ID: "pricing"})
	settle()
	s.Run("stats.open", nil)
	at(s, t, "stats in pricing", "pricing", "stats")
	s.Store().Apply(core.SessionFocused{ID: "billing"})
	settle()
	if windowDir(s) != b {
		t.Fatalf("the window didn't open again on billing's directory: %s", windowDir(s))
	}
	at(s, t, "billing", "billing", "")
	s.Press("<X1Mouse>")
	settle()
	if windowDir(s) != a {
		t.Errorf("back didn't open the window on pricing's directory: %s", windowDir(s))
	}
	at(s, t, "back to pricing", "pricing", "stats")
}

// A Lua plugin's page joins the history as a place: back closes it, forward opens it again with its command.
func TestLuaPageIsAPlace(t *testing.T) {
	s := apptest.LuaScene(t, `
local open = false
oriel.command("Board", function() open = true end, { desc = "the board" })
oriel.place.add{ name = "board", title = "Board", command = "Board", shown = function() return open end, close = function() open = false end }
`)
	s.Frame()
	s.Run("Board", nil)
	at(s, t, "the board opened", "", "board")
	s.Press("<X1Mouse>")
	at(s, t, "back", "", "")
	s.Press("<X2Mouse>")
	at(s, t, "forward", "", "board")
}

// A page gives way to what's put in front: a view shown from the sidebar or a command, or a file opened, closes the
// page over the layout. A view already open under the page is revealed, not toggled away.
func TestPagesGiveWayToViews(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	s := navScene(t, dir)
	shown := func() string { p, _ := uireg.ShownPlace(s.Registry()); return p }
	explorer := registry.Args{"id": kit.ExplorerView, "place": "dock", "edge": "left", "title": "Explorer"}

	s.Run("settings.open", nil)
	s.Frame()
	s.Run("view.toggle", explorer)
	s.Frame()
	if p := shown(); p != "" {
		t.Errorf("%s stayed over the Explorer", p)
	}
	if !kit.Showing(s.Host(), kit.ExplorerView, "") {
		t.Error("the Explorer didn't open")
	}

	s.Run("stats.open", nil)
	s.Frame()
	s.Run("view.toggle", explorer)
	s.Frame()
	if p := shown(); p != "" || !kit.Showing(s.Host(), kit.ExplorerView, "") {
		t.Errorf("the Explorer open under %q was toggled away, or the page stayed", p)
	}

	s.Run("settings.open", nil)
	s.Frame()
	s.Run("editor.open", registry.Args{"path": "a.txt"})
	s.Frame()
	if p := shown(); p != "" {
		t.Errorf("%s stayed over the file opened", p)
	}
}
