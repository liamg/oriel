package app_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/luaconfig"
)

// A plugin's own source, polled while subscribed: on_change gets each reading, get the latest, and the source is
// read no more once its subscription closes.
func TestLuaSourceAndSubscribe(t *testing.T) {
	s := apptest.LuaScene(t, `
reads, seen = 0, nil
oriel.source{ name = "demo.count", poll = 0.05, read = function(args) reads = reads + 1; return { n = reads, who = args.who } end }
sub = oriel.subscribe("demo.count", { session = "app", who = "ada", max_age = 0.05, on_change = function(v) seen = v end })
`)
	s.Until("two readings", func() bool { return luaconfig.Show(s.Lua(`seen and seen.n >= 2`)[0]) == "true" })
	if got := luaconfig.Show(s.Lua(`sub:get().who`)[0]); got != `"ada"` {
		t.Errorf("the reading's args: %s", got)
	}
	if got := luaconfig.Show(s.Lua(`(function() for _, n in ipairs(oriel.sources()) do if n == "demo.count" then return true end end return false end)()`)[0]); got != "true" {
		t.Errorf("oriel.sources() lacks demo.count: %s", got)
	}
	s.Lua(`(function() sub:close(); before = reads end)()`)
	for range 20 { // four of its polls
		s.Quiet()
		time.Sleep(10 * time.Millisecond)
	}
	if got := luaconfig.Show(s.Lua(`reads <= before + 1`)[0]); got != "true" { // one read may have been under way
		t.Errorf("read on after its subscription closed: %s then %s", luaconfig.Show(s.Lua(`before`)[0]), luaconfig.Show(s.Lua(`reads`)[0]))
	}
}

// files reports what changed under a folder.
func TestLuaSubscribeFiles(t *testing.T) {
	dir := t.TempDir()
	s := apptest.LuaScene(t, `changed = nil
sub = oriel.subscribe("files", { session = "app", path = "`+filepath.ToSlash(dir)+`", tree = true, on_change = function(v) changed = v end })`)
	s.Until("the first reading", func() bool { return luaconfig.Show(s.Lua(`changed ~= nil`)[0]) == "true" })
	os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x"), 0o644)
	s.Until("x.txt", func() bool { return strings.Contains(luaconfig.Show(s.Lua(`changed`)[0]), "x.txt") })
	if got := s.LuaErr(`oriel.subscribe("nope", {})`); !strings.Contains(got, "no source") {
		t.Errorf("an unknown source: %s", got)
	}
}
