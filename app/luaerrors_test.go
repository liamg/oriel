package app_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

// writeConfig writes files under the scene's config directory.
func writeConfig(s *apptest.Scene, t *testing.T, files map[string]string) {
	t.Helper()
	for name, src := range files {
		p := filepath.Join(s.ConfigDir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func luaFrames(detail string) []string {
	var out []string
	for _, f := range script.ParseTrace(detail) {
		if !f.Go {
			out = append(out, f.Loc())
		}
	}
	return out
}

// Fixture configs with a syntax error, a runtime error in a hook, an error inside a nested `require`, and an error
// raised through an `oriel` API call each give the right `file:line`, traceback frames and diagnostic location.
func TestLuaErrorFixtures(t *testing.T) {
	for _, c := range []struct {
		name   string
		files  map[string]string
		fire   string // an event to fire after loading
		notice string // the notice's ID prefix
		at     string
		frames []string
		action string
	}{
		{name: "syntax error", files: map[string]string{"init.lua": "local L = oriel\nL.option.tabstop = = 4\n"},
			notice: "lua.load", at: "init.lua:2", action: "Edit init.lua"},
		{name: "runtime error in a hook", files: map[string]string{"init.lua": `local L = oriel
L.on("Permission", function(ev)
  if ev.tool == "Bash" and ev.args:match("^go test ") then
    return "allow"
  end
end)
`}, fire: "Permission", notice: "failed.hook.Permission", at: "init.lua:3", frames: []string{"init.lua:3", "oriel.on"}, action: "Open at line"},
		{name: "nested require", files: map[string]string{
			"init.lua":           "local L = oriel\nlocal t = require('my.tickets')\nL.on('CommentSend', t.expand)\n",
			"lua/my/tickets.lua": "local M = {}\nfunction M.expand(ev)\n  local s = require('my.linear').link(ev.body)\n  return s\nend\nreturn M\n",
			"lua/my/linear.lua":  "local M = {}\nfunction M.link(body)\n  local s = body .. nil\n  return s\nend\nreturn M\n",
		}, fire: "CommentSend", notice: "failed.hook.CommentSend", at: "lua/my/linear.lua:3",
			frames: []string{"lua/my/linear.lua:3", "lua/my/tickets.lua:3", "oriel.on"}, action: "Open at line"},
		{name: "through an API call", files: map[string]string{"init.lua": "local L = oriel\n\nL.agent.tool({ desc = 'no name' })\n"},
			notice: "lua.load", at: "init.lua:3", frames: []string{"oriel.agent.tool", "init.lua:3"}, action: "Edit init.lua"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := apptest.LuaScene(t, "")
			s.WriteConfig(c.files)
			s.ReloadLua()
			if c.fire != "" {
				s.Registry().Fire(context.Background(), c.fire, "", map[string]any{"tool": "Bash", "body": "x"})
			}
			n, ok := apptest.LastNotice(s.Store().State(), c.notice)
			if !ok {
				t.Fatalf("no %s notice in %+v", c.notice, s.Store().State().Notifications)
			}
			if n.Location != c.at || n.Severity != core.SevError {
				t.Errorf("the notice is at %q (%s), want %s: %s", n.Location, n.Severity, c.at, n.Text)
			}
			if got := luaFrames(n.Detail); c.frames != nil && !slices.Equal(got[:min(len(got), len(c.frames))], c.frames) {
				t.Errorf("frames %v, want %v first\n%s", got, c.frames, n.Detail)
			}
			file, line := script.SplitLoc(c.at)
			path := filepath.Join(s.ConfigDir, file)
			if a, ok := apptest.Action(n, c.action); !ok || a.Command != "editor.open" || a.Args["path"] != path || a.Args["line"] != line {
				t.Errorf("no %q action to %s:%d: %+v", c.action, path, line, n.Actions)
			}
			ds, _ := s.Registry().Diagnostics()
			if len(ds) != 1 || ds[0].File != path || ds[0].Line != line-1 || ds[0].Owner != "lua" || ds[0].Severity != registry.SevError {
				t.Errorf("diagnostics %+v, want one at %s:%d", ds, path, line-1)
			}
		})
	}
}

// A failing `init.lua` on reload leaves the previous keymaps and hooks working.
func TestBrokenReloadKeepsTheLastGoodConfig(t *testing.T) {
	s := apptest.LuaScene(t, `local L = oriel
L.option.tabstop = 4
L.keymap.set("n", "<leader>rr", "agent.review")
L.on("Permission", function() return "allow" end)
`)
	x := lua(s)
	for _, broken := range []string{
		"local L = oriel\nL.option.tabstop = = 8\n",                                                        // doesn't parse
		"local L = oriel\nL.option.tabstop = 8\nL.keymap.set('n', 'gx', 'nope')\nlocal x = nil\nx.y = 1\n", // runs part way
	} {
		s.WriteConfig(map[string]string{"init.lua": broken})
		x.Reload(true)
		if v, _ := s.Registry().Fire(context.Background(), "Permission", "", map[string]any{}); v.Value != "allow" {
			t.Errorf("the last good hook is gone: %+v", v)
		}
		var keys []string
		for _, k := range s.Registry().Keymaps() {
			if strings.HasPrefix(k.Source, "init.lua:") {
				keys = append(keys, k.Keys)
			}
		}
		if !slices.Equal(keys, []string{"<Space>rr"}) {
			t.Errorf("keymaps %v, want the last good config's", keys)
		}
		if v, src := s.Option("editor.tab_stop", registry.At{}); v != 4 || src != "init.lua:2" {
			t.Errorf("tabstop %v from %s, want the last good config's 4", v, src)
		}
		n, _ := apptest.LastNotice(s.Store().State(), "lua.load")
		if !strings.Contains(n.Text, "the last good config is still running") {
			t.Errorf("the notice doesn't say so: %q", n.Text)
		}
	}

	// A fix replaces it: the option it no longer sets goes back to its default.
	s.WriteConfig(map[string]string{"init.lua": "local L = oriel\nL.keymap.set('n', 'gx', 'agent.review')\n"})
	x.Reload(true)
	if v, src := s.Option("editor.tab_stop", registry.At{}); v == 4 || src != "" {
		t.Errorf("tabstop %v from %q after the config stopped setting it", v, src)
	}
	if v, _ := s.Registry().Fire(context.Background(), "Permission", "", map[string]any{}); v.Decided {
		t.Errorf("the replaced config's hook still decides: %+v", v)
	}
}

// At startup Oriel opens, the toast's link opens the file at the line, and saving a fix reloads it and clears the
// toast.
func TestBrokenConfigAtStartup(t *testing.T) {
	s := apptest.LuaScene(t, "local L = oriel\nL.keymap.set('n', 'ga', 'agent.review')\nlocal tickets\ntickets.setup()\n")
	s.Frame()
	s.Frame()
	text := s.Text()
	for _, want := range []string{"Edit init.lua", "Show trace", "init.lua:4"} {
		if !strings.Contains(text, want) {
			t.Errorf("the toast lacks %q:\n%s", want, text)
		}
	}
	if !slices.ContainsFunc(s.Registry().Keymaps(), func(k registry.Keymap) bool { return k.Source == "init.lua:2" }) {
		t.Errorf("the part before the error isn't running")
	}
	n, _ := apptest.LastNotice(s.Store().State(), "lua.load")
	a, _ := apptest.Action(n, "Edit init.lua")
	s.Run(a.Command, registry.Args(a.Args))
	s.Frame()
	if w, ok := s.Editor().Window(""); !ok || w.Path != filepath.Join(s.ConfigDir, "init.lua") || w.Cursor.Line != 3 {
		t.Errorf("the link didn't open init.lua at line 4: %+v", w)
	}
	if !strings.Contains(s.Text(), "attempt to index") {
		t.Errorf("no diagnostic on the line:\n%s", s.Text())
	}

	s.WriteConfig(map[string]string{"init.lua": "local L = oriel\nL.on('Commit', function() return end)\n"})
	x := lua(s)
	if !x.Changed() {
		t.Fatal("the save went unseen")
	}
	x.Reload(true)
	for _, row := range noticesWith(s.Store().State(), "lua.load") {
		if !row.Dismissed {
			t.Errorf("the toast is still up: %+v", row)
		}
	}
	if ds, _ := s.Registry().Diagnostics(); len(ds) != 0 {
		t.Errorf("diagnostics after the fix: %+v", ds)
	}
}

// A hook failing three times is off, shown in `:Hooks`, and back on after "Turn back on".
func TestFailingHookTurnedOff(t *testing.T) {
	s := apptest.LuaScene(t, "local L = oriel\nL.on('Permission', function(ev)\n  return ev.args:match('x')\nend)\n")
	fire := func() { s.Registry().Fire(context.Background(), "Permission", "", map[string]any{"tool": "Bash"}) }
	for range 3 {
		fire()
	}
	h := configHooks(s)[0]
	if h.Off == nil {
		t.Fatalf("on after 3 errors: %+v", h)
	}
	rows := noticesWith(s.Store().State(), "failed.hook."+h.ID)
	if len(rows) != 1 || rows[0].Count != 3 || !strings.Contains(rows[0].Text, "Permission hook turned off after 3 errors") {
		t.Fatalf("notices %+v", rows)
	}
	turnOn, ok := apptest.Action(rows[0], "Turn back on")
	if !ok {
		t.Fatalf("no Turn back on: %+v", rows[0].Actions)
	}
	s.Run("Hooks", nil)
	s.Frame()
	s.Frame()
	if text := s.Text(); !strings.Contains(text, "off: init.lua:3: attempt to index") {
		t.Errorf(":Hooks doesn't show it off:\n%s", text)
	}
	s.Run(turnOn.Command, registry.Args(turnOn.Args))
	if configHooks(s)[0].Off != nil {
		t.Fatal("still off")
	}
	fire()
	if configHooks(s)[0].Calls != 4 {
		t.Errorf("it didn't run again: %+v", configHooks(s)[0])
	}
}

// A Lua view that errors draws its error, and 3 frames in a row turn it off, the built-in back in its place.
func TestFailingViewTurnedOff(t *testing.T) {
	s := apptest.LuaScene(t, "local L = oriel\nL.statusline(function(w)\n  return L.ui.row{ L.ui.text(w.nope.x) }\nend)\n")
	for range 3 {
		s.Frame()
	}
	if off := s.Registry().TurnedOff(); len(off) != 1 || off[0].Kind != registry.KindView || off[0].ID != kit.StatusLineView {
		t.Fatalf("turned off: %+v", off)
	}
	n, _ := apptest.LastNotice(s.Store().State(), "failed.view."+kit.StatusLineView)
	if n.Location != "init.lua:3" || !strings.Contains(n.Text, "turned off after 3 errors") {
		t.Errorf("notice %+v", n)
	}
}

// Diagnostics show in Problems, and a click there opens the file at the line.
func TestProblems(t *testing.T) {
	s := apptest.LuaScene(t, "local L = oriel\n\nL.option.tabstop = = 4\n")
	s.Registry().SetDiagnostics("gopls", filepath.Join(s.ConfigDir, "x.go"), []registry.Diagnostic{{Line: 9, Severity: registry.SevWarning, Message: "x declared and not used"}})
	s.Run("Problems", nil)
	s.Frame()
	s.Frame()
	text := s.Text()
	for _, want := range []string{"PROBLEMS", "init.lua:3", "x.go:10", "x declared and not used", "lua", "gopls"} {
		if !strings.Contains(text, want) {
			t.Errorf("Problems lacks %q:\n%s", want, text)
		}
	}
}
