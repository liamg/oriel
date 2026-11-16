package app_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/luaconfig"
	"github.com/liamg/oriel/registry/services"
)

// A plugin's own source, polled while subscribed: on_change gets each reading, get the latest, and once its
// subscription closes the manager no longer keeps it fresh.
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
	s.Lua(`sub:close()`)
	s.Quiet()
	w, _ := services.WatchesOf(s.Registry())
	for _, st := range w.App().Stats() {
		if strings.Contains(st.Key, "demo.count") && (len(st.Subs) > 0 || st.State == "polling" || st.State == "reading") {
			t.Errorf("its subscription closed, the source is still kept fresh: %s, %d subscribers", st.State, len(st.Subs))
		}
	}
}

// files reports what changed under a folder.
func TestLuaSubscribeFiles(t *testing.T) {
	dir := t.TempDir()
	s := apptest.LuaScene(t, `changed = nil
sub = oriel.subscribe("files", { session = "app", path = "`+filepath.ToSlash(dir)+`", tree = true, on_change = function(v) changed = v end })`)
	s.Until("the first reading", func() bool { return luaconfig.Show(s.Lua(`changed ~= nil`)[0]) == "true" })
	s.WriteFiles(dir, map[string]string{"x.txt": "x"})
	s.Until("x.txt", func() bool { return strings.Contains(luaconfig.Show(s.Lua(`changed`)[0]), "x.txt") })
	if got := s.LuaErr(`oriel.subscribe("nope", {})`); !strings.Contains(got, "no source") {
		t.Errorf("an unknown source: %s", got)
	}
}
