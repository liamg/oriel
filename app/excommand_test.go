package app_test

import (
	"testing"

	"github.com/liamg/oriel/app/apptest"
)

// An ex command is an alias of a command's id: a plugin's ex = "Hi" makes :Hi {text} run it, with the text as the
// argument ex_arg names.
func TestAPluginsExNameRunsItsCommand(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.command("acme.greeting.say", function(a) oriel.notify("hi " .. (a.who or "?")) end,
  { desc = "Say hi", ex = "Hi", ex_arg = "who" })`)
	s.OpenFile(writeFile(t, "notes.txt", "notes\n"))
	s.EditorKeys(":Hi bob<CR>")
	if noticed(s, "hi bob") != 1 {
		t.Errorf(":Hi bob didn't run acme.greeting.say with who; notices: %v", noticeTexts(s.Store().State().Notifications))
	}
}

// A built-in's ex name runs it with what was typed: :Settings ui opens the settings page at its ui section.
func TestABuiltinsExNameRunsItsCommand(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.OpenFile(writeFile(t, "notes.txt", "notes\n"))
	s.EditorKeys(":Settings ui<CR>")
	s.Frame()
	if !placeShown(s, "settings") {
		t.Fatal(":Settings ui didn't open the settings page")
	}
	if !apptest.HasAll(s.Text(), "ui.theme") {
		t.Errorf(":Settings ui isn't at the ui section:\n%s", s.Text())
	}
}
