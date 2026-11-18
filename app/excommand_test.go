package app_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
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

// A plugin's command that declares its args refuses a run with a key it doesn't take or a value of the wrong type,
// naming them, and runs with what it takes; one with a type that isn't one fails to register, saying so.
func TestAPluginsCommandChecksItsArgs(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.command("acme.greeting.say", function(a) oriel.notify("hi " .. a.who .. " x" .. a.times) end,
  { desc = "Say hi", args = { { name = "who", type = "string", required = true }, { name = "times", type = "int" } } })
local ok, err = pcall(oriel.command, "acme.bad.say", function() end, { args = { { name = "x", type = "number" } } })
oriel.notify("bad: " .. tostring(err))`)
	run := func(a registry.Args) error { return s.Registry().Run(context.Background(), "acme.greeting.say", a) }
	if err := run(registry.Args{"who": "bob", "times": 2.0}); err != nil || noticed(s, "hi bob x2") != 1 {
		t.Fatalf("a run with what it takes: %v; notices %v", err, noticeTexts(s.Store().State().Notifications))
	}
	if err := run(registry.Args{"whom": "bob"}); err == nil || !strings.Contains(err.Error(), `"whom" isn't one it takes: it takes who, times`) {
		t.Errorf("a misspelled key: %v", err)
	}
	if err := run(registry.Args{"who": "bob", "times": "twice"}); err == nil || !strings.Contains(err.Error(), `"times" is a string, want int`) {
		t.Errorf("a value of the wrong type: %v", err)
	}
	if err := run(registry.Args{"times": 1.0}); err == nil || !strings.Contains(err.Error(), `"who" is required`) {
		t.Errorf("a required key missing: %v", err)
	}
	if !slices.ContainsFunc(noticeTexts(s.Store().State().Notifications), func(n string) bool { return strings.Contains(n, `no type "number"`) }) {
		t.Errorf("a type that isn't one: notices %v", noticeTexts(s.Store().State().Notifications))
	}
}
