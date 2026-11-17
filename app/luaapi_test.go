package app_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/luaconfig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
)

// A callback answering something once, a side question's answer here, that raises says so in a notice at its line,
// as a click's callback does, rather than failing unseen.
func TestAFailingAnswerCallbackSaysSo(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.agent.fork({ prompt = "why?" }, function(answer, err)
  error("boom: " .. tostring(err))
end)
`)
	s.Until("the callback's notice", func() bool {
		_, ok := apptest.LastNotice(s.Store().State(), "lua.callback.")
		return ok
	})
	n, _ := apptest.LastNotice(s.Store().State(), "lua.callback.")
	if n.Location != "init.lua:2" || !strings.Contains(n.Text, "boom: no session's agent to ask") {
		t.Errorf("the failing callback's notice: %+v", n)
	}
}

// L.regex is PCRE2 with string.find's conventions (docs/api.md), so for patterns both dialects read the same, Lua's
// own string functions are the oracle.
func TestLuaRegex(t *testing.T) {
	s := apptest.LuaScene(t, "")
	for _, c := range []struct{ pcre, lua, subject string }{
		{"(b+)", "(b+)", "abbbc"},
		{"b", "b", "abc"},
		{"z", "z", "abc"},
		{"^(a)(b)", "^(a)(b)", "abc"},
	} {
		got := s.Lua(`{ L.regex.find("` + c.pcre + `", "` + c.subject + `") }`)
		want := s.Lua(`{ string.find("` + c.subject + `", "` + c.lua + `") }`)
		if luaconfig.Show(got[0]) != luaconfig.Show(want[0]) {
			t.Errorf("find(%q, %q) = %s, string.find gives %s", c.pcre, c.subject, luaconfig.Show(got[0]), luaconfig.Show(want[0]))
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
		if got := luaconfig.Show(s.Lua(line)[0]); got != want {
			t.Errorf("%s = %s, want %s", line, got, want)
		}
	}
	if err := s.LuaErr(`L.regex.find("(", "x")`); !strings.Contains(err, "oriel.regex.find") {
		t.Errorf("a bad pattern's error: %s", err)
	}
	if err := s.LuaErr(`L.regex.find("(?(1)a|b)", "x")`); !strings.Contains(err, "not supported") {
		t.Errorf("an unsupported feature's error: %s", err)
	}
}

// The root functions: json, read, exec, cmd, notify, log.
func TestLuaRootFunctions(t *testing.T) {
	s := apptest.LuaScene(t, `local L = oriel
L.command("Remember", function(args) L.remembered = args.x end, { desc = "keep x" })
`)
	file := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ line, want string }{
		{`L.json_decode('{"a":[1,2]}').a[2]`, "2"},
		{`L.json_encode({ a = 1 })`, `"{\"a\":1}"`},
		{`L.json_encode("hello")`, `"\"hello\""`},
		{`L.json_decode('"hello"')`, `"hello"`},
		{`L.read("` + file + `")`, `"hello"`},
		{`select(2, L.read("/nonexistent/x"))`, `"open /nonexistent/x: no such file or directory"`},
		{`L.exec({ "echo", "hi" })`, `"hi\n"`},
		{`L.exec({ "pwd" }, { cwd = "/" })`, `"/\n"`},
		{`select(2, L.exec({ "false" }))`, `"false: exit status 1: "`},
		{`(L.exec({ "false" }))`, "nil"},
		{`(L.exec({ "/no/such/program" }))`, "nil"},
		{`(L.process.spawn("/no/such/program"))`, "nil"},
		{`select(2, L.process.spawn("/no/such/program")) ~= nil`, "true"},
		{`(L.wasm.load("/no/such/module.wasm"))`, "nil"},
		{`select(2, L.wasm.load("/no/such/module.wasm")) ~= nil`, "true"},
		{`(L.run("Remember", { x = 7 })) == nil`, "true"},
		{`L.remembered`, "7"},
	} {
		if got := luaconfig.Show(s.Lua(c.line)[0]); got != c.want {
			t.Errorf("%s = %s, want %s", c.line, got, c.want)
		}
	}
	for line, want := range map[string]string{
		`L.json_decode("{")`:       "oriel.json_decode:",
		`L.exec({})`:               "oriel.exec: no program",
		`L.run("no.such.command")`: "oriel.run:",
	} {
		if err := s.LuaErr(line); !strings.Contains(err, want) {
			t.Errorf("%s: %s, want %q in it", line, err, want)
		}
	}

	s.Lua(`L.notify("deployed", "warn")`)
	n, ok := apptest.LastNotice(s.Store().State(), "lua.")
	if !ok || n.Text != "deployed" || n.Severity != core.SevWarning || n.Location != "test:1" {
		t.Errorf("notify: %+v", n)
	}
	s.Lua(`L.notify("bad", "error")`)
	if n, _ := apptest.LastNotice(s.Store().State(), "lua."); n.Severity != core.SevError {
		t.Errorf("notify at error: %+v", n)
	}
	s.Lua(`L.log("from lua", "warn")`)
	recs := s.Logs.Ring.Records()
	if !slices.ContainsFunc(recs, func(r logs.Record) bool {
		return r.Msg == "from lua" && r.Level == slog.LevelWarn && r.Source == "test:1"
	}) {
		t.Errorf("no log record: %+v", recs)
	}
}

// L.diagnostics publishes into the registry's diagnostics under an owner the config scopes, gone when the config reloads.
func TestLuaDiagnostics(t *testing.T) {
	s := apptest.LuaScene(t, `local L = oriel
L.diagnostics.set("lint", "/p/a.go", { { line = 3, col = 2, end_col = 5, severity = "warning", message = "unused" } })
`)
	ds, _ := s.Registry().Diagnostics()
	if len(ds) != 1 || ds[0] != (registry.Diagnostic{File: "/p/a.go", Line: 2, From: 1, To: 4, Severity: registry.SevWarning, Message: "unused", Owner: "init.lua:lint"}) {
		t.Fatalf("diagnostics %+v", ds)
	}
	if got := luaconfig.Show(s.Lua(`L.diagnostics.list()[1].line`)[0]); got != "3" {
		t.Errorf("list's line %s", got)
	}
	if err := s.LuaErr(`L.diagnostics.set("x", "/f", { 1 })`); !strings.Contains(err, "a diagnostic is a table") {
		t.Errorf("a bad item: %s", err)
	}
	for src, want := range map[string]string{
		`L.diagnostics.set("lint", "/p/a.go", { line = 1, message = "one, not a list" })`: "a list of tables",
		`L.diagnostics.set("lint", "/p/a.go", "unused")`:                                  "a list of tables",
		`L.diagnostics.set("lint", "/p/a.go", { { message = "no line" } })`:               "needs a line",
	} {
		if err := s.LuaErr(src); !strings.Contains(err, want) {
			t.Errorf("%s: %q", src, err)
		}
	}
	if ds, _ := s.Registry().Diagnostics(); len(ds) != 1 {
		t.Errorf("bad input cleared or changed them: %+v", ds)
	}
	s.Lua(`L.diagnostics.set("lint", "/p/a.go", {})`)
	if ds, _ := s.Registry().Diagnostics(); len(ds) != 0 {
		t.Errorf("an empty list didn't clear them: %+v", ds)
	}
	s.WriteConfig(map[string]string{"init.lua": ""})
	s.ReloadLua()
	if ds, _ := s.Registry().Diagnostics(); len(ds) != 0 {
		t.Errorf("after a reload: %+v", ds)
	}
}

// Options from Lua take each type's values, and say what's wrong with one that doesn't fit.
func TestLuaOptionTypes(t *testing.T) {
	s := apptest.LuaScene(t, `local L = oriel
L.option.tabstop = 4
L.option.expandtab = true
L.option["ui.variant"] = "light"
L.option["ui.variant"] = nil
`)
	if v, _ := s.Option("editor.tab_stop", registry.At{}); v != 4 {
		t.Errorf("tabstop %v", v)
	}
	if v, _ := s.Option("editor.expand_tab", registry.At{}); v != true {
		t.Errorf("expandtab %v", v)
	}
	for line, want := range map[string]string{
		`L.option.tabstop = "four"`: "tab_stop",
		`L.option.tabstop = 1.5`:    "tab_stop",
		`L.option.expandtab = 3`:    "expand_tab",
	} {
		if err := s.LuaErr(line); !strings.Contains(err, want) {
			t.Errorf("%s: %s", line, err)
		}
	}
	if got := luaconfig.Show(s.Lua(`L.option.tabstop`)[0]); got != "4" {
		t.Errorf("reading L.option.tabstop: %s", got)
	}
}

// oriel.ui nodes build: rows, columns, text in each style, spacers, indicators with bars, lists through a row function,
// and a built-in view by id.
func TestLuaUINodes(t *testing.T) {
	s := apptest.LuaScene(t, `local L = oriel
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
	if err := os.WriteFile(file, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Run("editor.open", registry.Args{"path": file}) // the mode shows while an editor does
	s.Frame()
	s.Frame()
	text := s.Text()
	for _, want := range []string{"NORMAL", "dim one", "42", "CTX", "90%", "DEPLOY", "2", "item a", "item b", "plain", "nested"} {
		if !strings.Contains(text, want) {
			t.Errorf("the status line lacks %q:\n%s", want, text)
		}
	}
	if off := s.Registry().TurnedOff(); len(off) != 0 {
		t.Errorf("turned off: %+v", off)
	}
}

// A Lua callback that draws and throws (a list's row) shows why in its place, and the rest still draws.
func TestLuaUIRowErrorShowsInPlace(t *testing.T) {
	s := apptest.LuaScene(t, `local L = oriel
L.statusline(function(w)
  return L.ui.row{
    L.ui.list({ "a", "b" }, function(x)
      if x == "a" then error("row broke") end
      return L.ui.text("item " .. x)
    end),
  }
end)
`)
	file := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(file, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Run("editor.open", registry.Args{"path": file})
	s.Frame()
	s.Frame()
	if text := s.Text(); !strings.Contains(text, "row broke") || !strings.Contains(text, "item b") {
		t.Errorf("the status line:\n%s", text)
	}
}

// A ship gate whose check throws fails, saying why, rather than leaving the Ship page; and it's reported among the
// plugin's failures, so enough of them turn it off.
func TestLuaShipGateThatThrowsFails(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.ship.gate{ name = "lint", check = function() error("lint crashed") end }`)
	var failed []registry.Failure
	s.Registry().OnFailure(func(f registry.Failure) { failed = append(failed, f) })
	gs := registry.ShipGates(s.Registry(), "", "")
	if len(gs) != 1 || gs[0].State != registry.GateFail || !strings.Contains(gs[0].Note, "lint crashed") {
		t.Errorf("gates: %+v", gs)
	}
	if len(failed) != 1 || failed[0].Kind != "ship.gate" || failed[0].ID != "lint" {
		t.Errorf("failures: %+v", failed)
	}
}

// A per-frame callback that throws (a rail entry's badge) counts nothing and is reported, as a view's is.
func TestLuaBadgeThatThrowsIsReported(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.rail.add{ name = "todo", icon = "◆", title = "Todo", command = "Settings", badge = function() error("no count") end }`)
	var failed []registry.Failure
	s.Registry().OnFailure(func(f registry.Failure) { failed = append(failed, f) })
	for _, e := range registry.ContributionsOf[uireg.RailEntry](s.Registry()) {
		if e.Name == "todo" && e.Badge() != 0 {
			t.Error("a broken badge counted something")
		}
	}
	if len(failed) != 1 || failed[0].Kind != "rail.entry" || failed[0].ID != "todo" || !strings.Contains(failed[0].Err.Error(), "no count") {
		t.Errorf("failures: %+v", failed)
	}
}

// A decoration provider in Lua marks lines in the editor: a sign and virtual text, 1-based, and a chip.
func TestLuaDecorations(t *testing.T) {
	s := apptest.LuaScene(t, `local L = oriel
L.decorations.provider("todo", function(buf, top, bottom)
  return { { line = 2, sign = "T", hl = "warn", text = "todo here" }, { line = 3, text = "x = 1", bg = "bg.raised" }, "not a table" }
end)
`)
	file := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Run("editor.open", registry.Args{"path": file})
	s.Frame()
	s.Frame()
	ds := s.Decorations(0, 3)
	if len(ds[1]) != 2 || ds[1][0].Text != "T" || ds[1][1].Text != "todo here" {
		t.Errorf("line 2's decorations: %+v", ds)
	}
	if len(ds[2]) != 1 || ds[2][0].Text != "x = 1" || ds[2][0].Bg != "bg.raised" {
		t.Errorf("line 3's chip: %+v", ds[2])
	}
	if !strings.Contains(s.Text(), "todo here") {
		t.Errorf("not drawn:\n%s", s.Text())
	}
}

// HUD indicators and status rules are contributions their features will draw and ask; calling them gives what Lua made.
func TestLuaTatAndStatusRule(t *testing.T) {
	s := apptest.LuaScene(t, `local L = oriel
L.hud.indicator("deploys", function()
  return L.ui.indicator{ label = "DEPLOY", value = 3 }
end, { slot = "titlebar.right", every = "30s" })
L.status.rule("red", function(sess) if sess.red then return "error" end end)
`)
	var indicator uireg.Indicator
	for _, ind := range uireg.Indicators(s.Registry(), uireg.SlotTitleBar) {
		if ind.Name == "deploys" {
			indicator = ind
		}
	}
	if indicator.Slot != "titlebar.right" || indicator.Every.Seconds() != 30 {
		t.Errorf("indicator %+v", indicator)
	}
	if n, err := indicator.Build(nil); err != nil || n == nil {
		t.Errorf("building it: %v", err)
	}
	rule := s.Registry().Contributions("status.rule")[0].(registry.StatusRule)
	if v, err := rule.Rule(map[string]any{"red": true}); v != "error" || err != nil {
		t.Errorf("rule on a red session: %q %v", v, err)
	}
	if v, _ := rule.Rule(map[string]any{}); v != "" {
		t.Errorf("rule on another: %q", v)
	}
}

// Menu entries and status rules added in a loop are each their own: by the name the call gives (a menu entry's is
// its line and label without one), never only the line that made it, which every pass shares.
func TestLuaMenuAndStatusRulesFromALoop(t *testing.T) {
	s := apptest.LuaScene(t, `local L = oriel
for _, l in ipairs({ "One", "Two" }) do L.menu.add{ label = l, run = "noop" } end
L.menu.add{ name = "mine", label = "Three", run = "noop" }
for _, c in ipairs({ "error", "warn" }) do L.status.rule(c, function() return c end) end
L.status.rule("named", function() end)
`)
	var menus []string
	for _, c := range s.Registry().Contributions("menu.item") {
		menus = append(menus, c.(registry.MenuItem).Label)
	}
	if !slices.Contains(menus, "One") || !slices.Contains(menus, "Two") || !slices.Contains(menus, "Three") {
		t.Errorf("menu entries %q, want One, Two and Three", menus)
	}
	var rules []string
	for _, c := range s.Registry().Contributions("status.rule") {
		r := c.(registry.StatusRule)
		v, _ := r.Rule(nil)
		rules = append(rules, r.Name+"="+v)
	}
	if len(rules) != 3 || !slices.ContainsFunc(rules, func(r string) bool { return strings.HasSuffix(r, "=error") }) ||
		!slices.ContainsFunc(rules, func(r string) bool { return strings.HasSuffix(r, "=warn") }) || !slices.Contains(rules, "named=") {
		t.Errorf("status rules %q, want both of the loop's and the named one", rules)
	}
}

// L.language adds a language the editor picks by extension.
func TestLuaLanguage(t *testing.T) {
	s := apptest.LuaScene(t, `local L = oriel
L.language.add{ name = "mylang", extensions = { "myl" }, queries = { highlights = "(identifier) @variable" } }
`)
	langs, _ := s.Registry().Languages()
	i := slices.IndexFunc(langs, func(l registry.Language) bool { return l.Name == "mylang" })
	if i < 0 || !slices.Equal(langs[i].Extensions, []string{"myl"}) || langs[i].Queries["highlights"] == "" || langs[i].Source != "init.lua:2" {
		t.Errorf("languages: %+v", langs)
	}
	if err := s.LuaErr(`L.language.add{}`); !strings.Contains(err, "a language needs a name") {
		t.Errorf("no name: %s", err)
	}
}

// L.coverage reads and publishes the registry's line coverage, gone when the config reloads.
func TestLuaCoverage(t *testing.T) {
	s := apptest.LuaScene(t, `local L = oriel
L.coverage.set("jest", "/p/a.ts", { [3] = 2, [5] = 0 })
`)
	if got := s.Registry().CoverageFor("/p/a.ts"); len(got) != 2 || got[2] != 2 || got[4] != 0 {
		t.Fatalf("coverage %v", got)
	}
	if got := luaconfig.Show(s.Lua(`#L.coverage.lines("/p/a.ts", 1, 4)`)[0]); got != "1" {
		t.Errorf("lines 1–4: %s", got)
	}
	if got := luaconfig.Show(s.Lua(`L.coverage.lines("/p/a.ts", 1, 9)[2].hit`)[0]); got != "false" {
		t.Errorf("line 5's hit: %s", got)
	}
	s.WriteConfig(map[string]string{"init.lua": ""})
	s.ReloadLua()
	if got := s.Registry().CoverageFor("/p/a.ts"); len(got) != 0 {
		t.Errorf("after a reload: %v", got)
	}
}

// A Lua sign carries a right-click menu like a built-in's: each item runs its command with its args, and one whose
// command isn't there is left out.
func TestLuaSignMenu(t *testing.T) {
	s := apptest.LuaScene(t, `local L = oriel
L.decorations.provider("owners", function(buf, top, bottom)
  return { { line = 2, sign = "◆", tooltip = "Owner", menu = {
    { label = "Show owner", command = "owners.show", args = { who = "ana" } },
    { label = "Missing", command = "owners.nothing" },
  } } }
end)
`)
	var got registry.Args
	s.Registry().Command(registry.Command{ID: "owners.show", Run: func(_ registry.Ctx, a registry.Args) error { got = a; return nil }})
	file := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Run("editor.open", registry.Args{"path": file})
	s.Frame()
	s.Frame()
	menu := s.RightClick(s.TextPoint("◆"))
	if !strings.Contains(menu, "Show owner") || strings.Contains(menu, "Missing") {
		t.Fatalf("the sign's menu:\n%s", menu)
	}
	s.ClickOn("Show owner")
	if got.String("who") != "ana" {
		t.Errorf("Show owner ran with %v", got)
	}
}

// A Lua sign with on_hover shows only while the pointer is on its line, as the + that comments on a line does; with
// every_line it's on each line, and runs with the line it's on.
func TestLuaSignOnHover(t *testing.T) {
	s := apptest.LuaScene(t, `local L = oriel
L.decorations.provider("peek", function(buf, top, bottom)
  return { { line = 2, sign = "◆", tooltip = "Peek", command = "peek.show", on_hover = true },
    { every_line = true, sign = "✚", tooltip = "Note line {line}", command = "peek.note", args = { why = "x" }, on_hover = true } }
end)
`)
	var got registry.Args
	s.Registry().Command(registry.Command{ID: "peek.note", Run: func(_ registry.Ctx, a registry.Args) error { got = a; return nil }})
	file := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Run("editor.open", registry.Args{"path": file})
	s.Frame()
	s.Frame()
	if s.Has("◆") {
		t.Fatal("the sign shows with the pointer off its line")
	}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: s.TextPoint("two")})
	s.Frame()
	s.Frame()
	if !s.Has("◆") {
		t.Error("the sign doesn't show with the pointer on its line")
	}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: s.TextPoint("three")})
	s.Frame()
	s.Frame()
	if s.Has("◆") {
		t.Error("the sign still shows with the pointer on the next line")
	}
	if s.Find("Note line 3") == nil || s.Find("Note line 2") != nil {
		t.Fatal("the every-line sign isn't on line 3 alone, where the pointer is")
	}
	s.ClickOn("Note line 3")
	if got.Int("line") != 3 || got.String("why") != "x" {
		t.Errorf("the every-line sign ran with %v", got)
	}
}

// Options go last, and oriel.on and oriel.exec_async take them before the function too, as they always have
// (docs/api.md); options that aren't a table are a mistake in the call, which raises rather than being ignored.
func TestLuaOptionsGoLastOrBeforeTheFunction(t *testing.T) {
	s := apptest.LuaScene(t, `local L = oriel
L.on("BufWritePre", function() end, { pattern = "*.last", desc = "opts last" })
L.on("BufWritePre", { pattern = "*.before", desc = "opts before" }, function() end)
L.on("BufWritePre", function() end)
`)
	pats := map[string]string{}
	for _, h := range s.Registry().Hooks() {
		if h.Event == "BufWritePre" {
			pats[h.Label] = h.Pattern
		}
	}
	if pats["opts last"] != "*.last" || pats["opts before"] != "*.before" {
		t.Errorf("the hooks' patterns: %v", pats)
	}
	for line, want := range map[string]string{
		`L.on("BufWritePre", "*.go", function() end)`:             "options are a table",
		`L.on("BufWritePre", function() end, 7)`:                  "options are a table",
		`L.on("BufWritePre", { pattern = "*.go" })`:               "no function",
		`L.exec_async({ "echo", "hi" }, "quiet", function() end)`: "options are a table",
		`L.exec_async({ "echo", "hi" }, function() end, true)`:    "options are a table",
	} {
		if err := s.LuaErr(line); !strings.Contains(err, want) {
			t.Errorf("%s: %q, want %q in it", line, err, want)
		}
	}
	s.Lua(`L.exec_async({ "echo", "last" }, function(out) L.last = out end, { cwd = "/" })`)
	s.Lua(`L.exec_async({ "echo", "before" }, { cwd = "/" }, function(out) L.before = out end)`)
	s.Until("both answers", func() bool {
		return luaconfig.Show(s.Lua(`L.last`)[0]) == `"last\n"` && luaconfig.Show(s.Lua(`L.before`)[0]) == `"before\n"`
	})
}
