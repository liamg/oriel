package app

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/vim"
)

// eval is what a line of Lua returns in the config's VM, with L as oriel.
func (s *errScene) eval(t *testing.T, line string) []any {
	t.Helper()
	out, err := s.lua().vm.Eval(script.Chunk{Name: "test", Source: []byte("local L = oriel; return " + line)})
	if err != nil {
		t.Fatalf("%s: %v", line, err)
	}
	return out
}

// evalErr is the error a line of Lua raises.
func (s *errScene) evalErr(t *testing.T, line string) string {
	t.Helper()
	_, err := s.lua().vm.Eval(script.Chunk{Name: "test", Source: []byte("local L = oriel; " + line)})
	if err == nil {
		t.Fatalf("%s: no error", line)
	}
	return err.Error()
}

// L.regex is PCRE2 with string.find's conventions (docs/api.md), so for patterns both dialects read the same, Lua's
// own string functions are the oracle.
func TestLuaRegex(t *testing.T) {
	s := luaScene(t, "")
	for _, c := range []struct{ pcre, lua, subject string }{
		{"(b+)", "(b+)", "abbbc"},
		{"b", "b", "abc"},
		{"z", "z", "abc"},
		{"^(a)(b)", "^(a)(b)", "abc"},
	} {
		got := s.eval(t, `{ L.regex.find("`+c.pcre+`", "`+c.subject+`") }`)
		want := s.eval(t, `{ string.find("`+c.subject+`", "`+c.lua+`") }`)
		if show(got[0]) != show(want[0]) {
			t.Errorf("find(%q, %q) = %s, string.find gives %s", c.pcre, c.subject, show(got[0]), show(want[0]))
		}
	}
	for line, want := range map[string]string{
		`L.regex.match("^A", "abc", "i")`:                       "true",
		`L.regex.match("^A", "abc")`:                            "false",
		`#L.regex.find_all("\\d+", "a1b22c333")`:                "3",
		`L.regex.find_all("\\d+", "a1b22")[2].start`:            "4",
		`L.regex.find_all("\\d+", "a1b22")[2].finish`:           "5",
		`L.regex.find_all("(?<n>\\d)", "a1")[1].groups[1]`:      `"1"`,
		`select(2, L.regex.replace("(\\w+)@", "x@ y@", "$1!"))`: "2",
		`L.regex.replace("(\\w+)@", "x@ y@", "$1!")`:            `"x! y!"`,
		`L.regex.find("(?<=a)b", "ab")`:                         "2",
	} {
		if got := show(s.eval(t, line)[0]); got != want {
			t.Errorf("%s = %s, want %s", line, got, want)
		}
	}
	if err := s.evalErr(t, `L.regex.find("(", "x")`); !strings.Contains(err, "oriel.regex.find") {
		t.Errorf("a bad pattern's error: %s", err)
	}
	if err := s.evalErr(t, `L.regex.find("(?(1)a|b)", "x")`); !strings.Contains(err, "not supported") {
		t.Errorf("an unsupported feature's error: %s", err)
	}
}

// The root functions: json, read, exec, cmd, notify, log.
func TestLuaRootFunctions(t *testing.T) {
	s := luaScene(t, `local L = oriel
L.command("Remember", function(args) L.remembered = args.x end, { desc = "keep x" })
`)
	file := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(file, []byte("hello"), 0o644)
	for _, c := range []struct{ line, want string }{
		{`L.json('{"a":[1,2]}').a[2]`, "2"},
		{`L.json({ a = 1 })`, `"{\"a\":1}"`},
		{`L.read("` + file + `")`, `"hello"`},
		{`select(2, L.read("/nonexistent/x"))`, `"open /nonexistent/x: no such file or directory"`},
		{`L.exec({ "echo", "hi" })`, `"hi\n"`},
		{`L.exec({ "pwd" }, { cwd = "/" })`, `"/\n"`},
		{`(L.cmd("Remember", { x = 7 })) == nil`, "true"},
		{`L.remembered`, "7"},
	} {
		if got := show(s.eval(t, c.line)[0]); got != c.want {
			t.Errorf("%s = %s, want %s", c.line, got, c.want)
		}
	}
	for line, want := range map[string]string{
		`L.json("{")`:              "oriel.json:",
		`L.exec({ "false" })`:      "oriel.exec: false: exit status 1",
		`L.exec({})`:               "oriel.exec: no program",
		`L.cmd("no.such.command")`: "oriel.cmd:",
	} {
		if err := s.evalErr(t, line); !strings.Contains(err, want) {
			t.Errorf("%s: %s, want %q in it", line, err, want)
		}
	}

	s.eval(t, `L.notify("deployed", "warn")`)
	n, ok := lastNotice(s.store.State(), "lua.")
	if !ok || n.Text != "deployed" || n.Severity != core.SevWarning || n.Location != "test:1" {
		t.Errorf("notify: %+v", n)
	}
	s.eval(t, `L.notify("bad", "error")`)
	if n, _ := lastNotice(s.store.State(), "lua."); n.Severity != core.SevError {
		t.Errorf("notify at error: %+v", n)
	}
	s.eval(t, `L.log("from lua", "warn")`)
	recs := s.sh.cfg.Logs.Ring.Records()
	if !slices.ContainsFunc(recs, func(r logs.Record) bool {
		return r.Msg == "from lua" && r.Level == slog.LevelWarn && r.Source == "test:1"
	}) {
		t.Errorf("no log record: %+v", recs)
	}
}

// L.diagnostics publishes into the registry's diagnostics, gone when the config reloads.
func TestLuaDiagnostics(t *testing.T) {
	s := luaScene(t, `local L = oriel
L.diagnostics.set("lint", "/p/a.go", { { line = 3, col = 2, end_col = 5, severity = "warning", message = "unused" } })
`)
	ds, _ := s.reg.Diagnostics()
	if len(ds) != 1 || ds[0] != (registry.Diagnostic{File: "/p/a.go", Line: 2, From: 1, To: 4, Severity: registry.SevWarning, Message: "unused", Owner: "lint"}) {
		t.Fatalf("diagnostics %+v", ds)
	}
	if got := show(s.eval(t, `L.diagnostics.list()[1].line`)[0]); got != "3" {
		t.Errorf("list's line %s", got)
	}
	if err := s.evalErr(t, `L.diagnostics.set("x", "/f", { 1 })`); !strings.Contains(err, "a diagnostic is a table") {
		t.Errorf("a bad item: %s", err)
	}
	s.writeConfig(t, map[string]string{"init.lua": ""})
	s.lua().reload(false)
	if ds, _ := s.reg.Diagnostics(); len(ds) != 0 {
		t.Errorf("after a reload: %+v", ds)
	}
}

// Options from Lua take each type's values, and say what's wrong with one that doesn't fit.
func TestLuaOptionTypes(t *testing.T) {
	s := luaScene(t, `local L = oriel
L.option.tabstop = 4
L.option.expandtab = true
L.option["ui.variant"] = "light"
L.option["ui.variant"] = nil
`)
	if v, _ := s.option(t, "editor.tab_stop", registry.At{}); v != 4 {
		t.Errorf("tabstop %v", v)
	}
	if v, _ := s.option(t, "editor.expand_tab", registry.At{}); v != true {
		t.Errorf("expandtab %v", v)
	}
	for line, want := range map[string]string{
		`L.option.tabstop = "four"`: "tab_stop",
		`L.option.tabstop = 1.5`:    "tab_stop",
		`L.option.expandtab = 3`:    "expand_tab",
	} {
		if err := s.evalErr(t, line); !strings.Contains(err, want) {
			t.Errorf("%s: %s", line, err)
		}
	}
	if got := show(s.eval(t, `L.option.tabstop`)[0]); got != "4" {
		t.Errorf("reading L.option.tabstop: %s", got)
	}
}

// oriel.ui nodes build: rows, columns, text in each style, spacers, indicators with bars, lists through a row function,
// and a built-in view by id.
func TestLuaUINodes(t *testing.T) {
	s := luaScene(t, `local L = oriel
L.statusline(function(w)
  return L.ui.row{
    L.ui.mode(),
    L.ui.column{ L.ui.text("dim one", "dim"), L.ui.text(42, "warn") },
    L.ui.spacer(),
    L.ui.indicator{ label = "CTX", bar = 0.9, warn = 0.8 },
    L.ui.indicator{ label = "DEPLOY", value = 2, tone = "error" },
    L.ui.list({ "a", "b" }, function(x) return L.ui.text("item " .. x, "accent") end),
    "plain",
    { L.ui.text("nested", "ok") },
  }
end)
`)
	file := filepath.Join(t.TempDir(), "a.txt")
	os.WriteFile(file, []byte("one\n"), 0o644)
	s.run("editor.open", registry.Args{"path": file}) // the mode shows while an editor does
	s.frame()
	s.frame()
	text := s.frameText()
	for _, want := range []string{"NORMAL", "dim one", "42", "CTX", "90%", "DEPLOY", "2", "item a", "item b", "plain", "nested"} {
		if !strings.Contains(text, want) {
			t.Errorf("the status line lacks %q:\n%s", want, text)
		}
	}
	if off := s.reg.TurnedOff(); len(off) != 0 {
		t.Errorf("turned off: %+v", off)
	}
}

// A decoration provider in Lua marks lines in the editor: a sign and virtual text, 1-based.
func TestLuaDecorations(t *testing.T) {
	s := luaScene(t, `local L = oriel
L.decor.provider("todo", function(buf, top, bottom)
  return { { line = 2, sign = "T", hl = "warn", text = "todo here" }, "not a table" }
end)
`)
	file := filepath.Join(t.TempDir(), "a.txt")
	os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0o644)
	s.run("editor.open", registry.Args{"path": file})
	s.frame()
	s.frame()
	ds := s.editor().decorations(mustPane(t, s), 0, 3)
	if len(ds[1]) != 2 || ds[1][0].Text != "T" || ds[1][1].Text != "todo here" {
		t.Errorf("line 2's decorations: %+v", ds)
	}
	if !strings.Contains(s.frameText(), "todo here") {
		t.Errorf("not drawn:\n%s", s.frameText())
	}
}

// HUD indicators and status rules are contributions their features will draw and ask; calling them gives what Lua made.
func TestLuaTatAndStatusRule(t *testing.T) {
	s := luaScene(t, `local L = oriel
L.hud.indicator("deploys", { slot = "titlebar.right", every = "30s" }, function()
  return L.ui.indicator{ label = "DEPLOY", value = 3 }
end)
L.status.rule(function(sess) if sess.red then return "error" end end)
`)
	indicator := s.reg.Contributions("hud.indicator")[0].(registry.Indicator)
	if indicator.Slot != "titlebar.right" || indicator.Every.Seconds() != 30 {
		t.Errorf("indicator %+v", indicator)
	}
	if n, err := indicator.Build(nil); err != nil || n == nil {
		t.Errorf("building it: %v", err)
	}
	rule := s.reg.Contributions("status.rule")[0].(registry.StatusRule)
	if v, err := rule.Rule(map[string]any{"red": true}); v != "error" || err != nil {
		t.Errorf("rule on a red session: %q %v", v, err)
	}
	if v, _ := rule.Rule(map[string]any{}); v != "" {
		t.Errorf("rule on another: %q", v)
	}
}

// L.language adds a language the editor picks by extension.
func TestLuaLanguage(t *testing.T) {
	s := luaScene(t, `local L = oriel
L.language.add{ name = "mylang", extensions = { "myl" }, queries = { highlights = "(identifier) @variable" } }
`)
	langs, _ := s.reg.Languages()
	i := slices.IndexFunc(langs, func(l registry.Language) bool { return l.Name == "mylang" })
	if i < 0 || !slices.Equal(langs[i].Extensions, []string{"myl"}) || langs[i].Queries["highlights"] == "" || langs[i].Source != "init.lua:2" {
		t.Errorf("languages: %+v", langs)
	}
	if err := s.evalErr(t, `L.language.add{}`); !strings.Contains(err, "a language needs a name") {
		t.Errorf("no name: %s", err)
	}
}

func mustPane(t *testing.T, s *errScene) vim.Pane {
	t.Helper()
	p, ok := s.editor().e.Pane(s.editor().e.WindowID())
	if !ok {
		t.Fatal("no pane")
	}
	return p
}

// L.coverage reads and publishes the registry's line coverage, gone when the config reloads.
func TestLuaCoverage(t *testing.T) {
	s := luaScene(t, `local L = oriel
L.coverage.set("jest", "/p/a.ts", { [3] = 2, [5] = 0 })
`)
	if got := s.reg.CoverageFor("/p/a.ts"); len(got) != 2 || got[2] != 2 || got[4] != 0 {
		t.Fatalf("coverage %v", got)
	}
	if got := show(s.eval(t, `#L.coverage.lines("/p/a.ts", 1, 4)`)[0]); got != "1" {
		t.Errorf("lines 1–4: %s", got)
	}
	if got := show(s.eval(t, `L.coverage.lines("/p/a.ts", 1, 9)[2].hit`)[0]); got != "false" {
		t.Errorf("line 5's hit: %s", got)
	}
	s.writeConfig(t, map[string]string{"init.lua": ""})
	s.lua().reload(false)
	if got := s.reg.CoverageFor("/p/a.ts"); len(got) != 0 {
		t.Errorf("after a reload: %v", got)
	}
}
