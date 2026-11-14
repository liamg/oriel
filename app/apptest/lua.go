package apptest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/app/luaconfig"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/script"
)

// Lua evaluates line in the user's Lua (init.lua's VM), oriel as L, and answers what it returns.
func (s *Scene) Lua(line string) []any {
	s.T.Helper()
	out, err := PluginOf[*luaconfig.Lua](s, "lua").VM().Eval(script.Chunk{Name: "test", Source: []byte("local L = oriel; return " + line)})
	if err != nil {
		s.T.Fatalf("%s: %v", line, err)
	}
	return out
}

// LuaErr is the error line raises in the user's Lua; the test fails if it raises none.
func (s *Scene) LuaErr(line string) string {
	s.T.Helper()
	_, err := PluginOf[*luaconfig.Lua](s, "lua").VM().Eval(script.Chunk{Name: "test", Source: []byte("local L = oriel; " + line)})
	if err == nil {
		s.T.Fatalf("%s: no error", line)
	}
	return err.Error()
}

// LuaScene is a scene whose init.lua is initLua.
func LuaScene(t *testing.T, initLua string) *Scene {
	t.Helper()
	s := New(t, Options{WorkDir: t.TempDir(), Size: render.Point{X: 1400, Y: 900}})
	if err := os.WriteFile(filepath.Join(s.ConfigDir, "init.lua"), []byte(initLua), 0o644); err != nil {
		t.Fatal(err)
	}
	PluginOf[*luaconfig.Lua](s, "lua").Reload(false)
	return s
}

// ReloadLua loads init.lua again, quietly, as a change to it would.
func (s *Scene) ReloadLua() { PluginOf[*luaconfig.Lua](s, "lua").Reload(false) }
