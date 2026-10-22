package settings_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/settings"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// luaScene is a scene whose config is initLua, loaded.
func luaScene(t *testing.T, initLua string) *apptest.Scene {
	t.Helper()
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir(), Size: render.Point{X: 1400, Y: 900}})
	if err := os.WriteFile(filepath.Join(s.ConfigDir, "init.lua"), []byte(initLua), 0o644); err != nil {
		t.Fatal(err)
	}
	luaOf(s).Reload(false)
	return s
}

func luaOf(s *apptest.Scene) *settings.Lua { return apptest.PluginOf[*settings.Lua](s, "lua") }

// sample is the init.lua docs/configuration.md shows.
func sample(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "configuration.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	s = s[strings.Index(s, "## Example init.lua"):]
	s = s[strings.Index(s, "```lua\n")+len("```lua\n"):]
	return s[:strings.Index(s, "```")]
}

func noticesWith(st core.State, id string) []core.Notification {
	var out []core.Notification
	for _, n := range st.Notifications {
		if n.ID == id {
			out = append(out, n)
		}
	}
	return out
}

func configHooks(s *apptest.Scene) []registry.HookInfo {
	var out []registry.HookInfo
	for _, h := range s.Registry().Hooks() {
		if !strings.HasPrefix(h.Source, "builtin:") {
			out = append(out, h)
		}
	}
	return out
}

// Hooks veto and rewrite from Lua; :lua evaluates and completes; a save reloads the config, with a toast.
func TestLuaHooksREPLAndReload(t *testing.T) {
	s := luaScene(t, `local L = oriel
L.on("BufWritePre", { pattern = "*.md", desc = "no markdown" }, function(ev) return false, "not markdown, " .. ev.buf end)
L.on("CommentSend", function(ev) ev.body = ev.body:upper(); return ev end)
`)
	type comment struct{ Body string }
	c := &comment{Body: "ship it"}
	if _, errs := s.Registry().Fire(context.Background(), "CommentSend", "", c); len(errs) > 0 || c.Body != "SHIP IT" {
		t.Errorf("rewrite: %q %v", c.Body, errs)
	}
	v, _ := s.Registry().Fire(context.Background(), "BufWritePre", "README.md", &kit.BufWritePre{Buf: "README.md"})
	if !v.Veto || v.Reason != "not markdown, README.md" {
		t.Errorf("veto: %+v", v)
	}

	x := luaOf(s)
	x.Eval("1 + 1")
	x.Eval("oriel.api.version")
	x.Eval("oriel.nope()")
	texts, kinds := x.Repl()
	if texts[1] != "2" || texts[3] != `"0.5.0"` || kinds[5] != 2 {
		t.Errorf("repl %q %v", texts, kinds)
	}
	if names := x.Complete("oriel.ke"); !slices.Equal(names, []string{"keymap"}) {
		t.Errorf("completing oriel.ke: %v", names)
	}
	if names := x.Complete("oriel.regex.f"); !slices.Equal(names, []string{"find", "find_all"}) {
		t.Errorf("completing oriel.regex.f: %v", names)
	}

	path := filepath.Join(s.ConfigDir, "init.lua")
	os.WriteFile(path, []byte(`oriel.command("Hello", function() end, { desc = "hi" })`), 0o644)
	if !x.Changed() {
		t.Fatal("the save went unseen")
	}
	x.Reload(true)
	if hs := configHooks(s); len(hs) != 0 {
		t.Errorf("the old config's hooks are still there: %+v", hs)
	}
	if n := noticesWith(s.Store().State(), "lua.reload"); len(n) != 1 || !strings.Contains(n[0].Text, "reloaded init.lua") {
		t.Errorf("no reload toast: %+v", n)
	}
	os.WriteFile(path, []byte("local a = 1\nx = = 1\n"), 0o644)
	x.Reload(true)
	if n := noticesWith(s.Store().State(), "lua.load"); len(n) != 1 || n[0].Location != "init.lua:2" {
		t.Errorf("a syntax error's notice: %+v", n)
	}
}

// :Hooks and the REPL over the sample config, and :Inspect over the editor. The sample's
// status line shows the review ledger's LEDGER bar.
func TestLuaSnapshots(t *testing.T) {
	s := luaScene(t, sample(t))
	s.Registry().Fire(context.Background(), "Permission", "", map[string]any{"tool": "Bash", "input": "go test ./..."})
	s.Run("Hooks", nil)
	s.Run("lua", nil)
	x := luaOf(s)
	x.Eval("oriel.api.version")
	x.Eval("#oriel.ui.row{}.children")
	x.Eval("oriel.ledger.unread('pricing')")
	x.SetInput("oriel.agent.to")
	s.Frame()
	s.Golden("lua-hooks")
}

// `:lua error("x")` shows the error and trace under the input.
func TestREPLError(t *testing.T) {
	s := luaScene(t, "local M = {}\nfunction M.boom() error('deep') end\noriel.boom = M.boom\n")
	x := luaOf(s)
	s.Run("lua", registry.Args{"args": `error("x")`})
	s.Run("lua", registry.Args{"args": `oriel.boom()`})
	got, _ := x.Repl()
	want := []string{`error("x")`, "repl:1: x", "  repl:1: in main chunk", "oriel.boom()", "init.lua:2: deep", "  init.lua:2: in function 'boom'", "  repl:1: in main chunk"}
	if !slices.Equal(got, want) {
		t.Errorf("repl\n%q\nwant\n%q", got, want)
	}
	s.Run("lua", nil)
	s.Frame()
	s.Frame()
	if text := s.Text(); !strings.Contains(text, "init.lua:2: in function 'boom'") {
		t.Errorf("the trace isn't under the input:\n%s", text)
	}
	if ds, _ := s.Registry().Diagnostics(); len(ds) != 1 || ds[0].Line != 1 || !strings.HasSuffix(ds[0].File, "init.lua") {
		t.Errorf("a REPL call into the config marks its line: %+v", ds)
	}
}
