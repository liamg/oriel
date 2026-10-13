package script

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// config is a config directory: init.lua's source, and files under lua/.
func config(t *testing.T, files map[string]string) (*VM, string) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	vm := New(filepath.Join(dir, "lua"))
	t.Cleanup(vm.Close)
	return vm, dir
}

func loadErr(t *testing.T, vm *VM, src string) *Error {
	t.Helper()
	err := vm.Load(Chunk{Name: "init.lua", Source: []byte(src)})
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("want a *script.Error, got %v", err)
	}
	return e
}

func locs(fs []Frame) []string {
	var out []string
	for _, f := range fs {
		if !f.Go {
			out = append(out, f.Loc())
		}
	}
	return out
}

// errors.md: "A chunk loads with its source name, so every error says `init.lua:14` or `lua/my/tickets.lua:8`,
// not `[string]`."
func TestErrorLocations(t *testing.T) {
	vm, _ := config(t, map[string]string{
		"lua/my/mod.lua": "local M = {}\nfunction M.f()\n  local x = nil\n  return x.y\nend\nreturn M\n",
		"lua/my/bad.lua": "local M = {}\nM = = 1\nreturn M\n",
	})
	for _, c := range []struct {
		name, src, where string
		frames           []string
	}{
		{"syntax", "local a = 1\nx = = 1\n", "init.lua:2", nil},
		{"runtime", "local t\nlocal function g()\n  return t.x\nend\ng()\n", "init.lua:3", []string{"init.lua:3", "init.lua:5"}},
		{"nested require", "local m = require('my.mod')\nlocal function g()\n  m.f()\nend\ng()\n", "lua/my/mod.lua:4",
			[]string{"lua/my/mod.lua:4", "init.lua:3", "init.lua:5"}},
		{"syntax in a require", "require('my.bad')\n", "lua/my/bad.lua:2", []string{"init.lua:1"}},
	} {
		e := loadErr(t, vm, c.src)
		if got := e.Where(); got != c.where {
			t.Errorf("%s: at %q, want %s (%s)", c.name, got, c.where, e.Message)
		}
		if strings.Contains(e.Message, string(filepath.Separator)+"lua"+string(filepath.Separator)) {
			t.Errorf("%s: the message has an absolute path: %s", c.name, e.Message)
		}
		if got := locs(e.Frames()); c.frames != nil && strings.Join(got, " ") != strings.Join(c.frames, " ") {
			t.Errorf("%s: frames %v, want %v", c.name, got, c.frames)
		}
	}
}

// errors.md: "Frames inside the `oriel` API show the API call (`oriel.agent.tool`), not Go internals; Go frames are
// behind "show Go frames"."
func TestErrorThroughAPI(t *testing.T) {
	vm, _ := config(t, nil)
	vm.Bind("agent", map[string]GoFunc{"tool": func(a *Args) ([]any, error) { return nil, errors.New("a tool needs a name") }})
	e := loadErr(t, vm, "local function g()\n  oriel.agent.tool({})\nend\ng()\n")
	if e.Message != "init.lua:2: oriel.agent.tool: a tool needs a name" || e.Where() != "init.lua:2" {
		t.Errorf("message %q at %q", e.Message, e.Where())
	}
	fs := e.Frames()
	if got := locs(fs); strings.Join(got, " ") != "oriel.agent.tool init.lua:2 init.lua:4" {
		t.Errorf("frames %v", got)
	}
	gos := 0
	for _, f := range fs {
		if f.Go {
			gos++
			if strings.HasPrefix(f.Fn, "[G]") {
				t.Errorf("a gopher-lua frame shown as Go: %+v", f)
			}
		}
	}
	if gos == 0 {
		t.Errorf("no Go frames behind the API call: %+v", fs)
	}
	if !strings.Contains(e.Trace(), "script/error_test.go") {
		t.Errorf("the Go frames aren't in the trace:\n%s", e.Trace())
	}
}

// A Go function that panics is an error at its Lua call, its Go stack in the trace.
func TestAPIPanic(t *testing.T) {
	vm, _ := config(t, nil)
	vm.Bind("api", map[string]GoFunc{"boom": func(a *Args) ([]any, error) { var m map[string]int; m["x"]++; return nil, nil }})
	e := loadErr(t, vm, "\noriel.api.boom()\n")
	if !strings.HasPrefix(e.Message, "init.lua:2: oriel.api.boom: panic: assignment to entry in nil map") || e.Where() != "init.lua:2" {
		t.Errorf("message %q at %q", e.Message, e.Where())
	}
	if !strings.Contains(e.Trace(), "error_test.go") {
		t.Errorf("no Go stack:\n%s", e.Trace())
	}
}

// A call's error goes to OnError, once, with where it was.
func TestOnError(t *testing.T) {
	vm, _ := config(t, nil)
	var got []*Error
	vm.OnError = func(e *Error) { got = append(got, e) }
	var fn *Fn
	vm.Bind("", map[string]GoFunc{"keep": func(a *Args) ([]any, error) { fn = a.Fn(0); return nil, nil }})
	if err := vm.Load(Chunk{Name: "init.lua", Source: []byte("oriel.keep(function()\n  error('x')\nend)\n")}); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.Call(fn); err == nil {
		t.Fatal("no error")
	}
	if len(got) != 1 || got[0].Message != "init.lua:2: x" || got[0].Where() != "init.lua:2" {
		t.Errorf("OnError got %+v", got)
	}
}
