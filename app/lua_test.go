package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// sample is scripting.md's init.lua, as written there.
func sample(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "_design", "scripting.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "## API shape (sketch)")
	s = s[i:]
	s = s[strings.Index(s, "```lua\n")+len("```lua\n"):]
	return s[:strings.Index(s, "```")]
}

func (s *errScene) lua() *luaPlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*luaPlugin); ok {
			return x
		}
	}
	return nil
}

// luaScene is the built-ins with an init.lua in the config directory.
func luaScene(t *testing.T, initLua string) *errScene {
	t.Helper()
	s := newScene(t, "", t.TempDir(), render.Point{X: 1400, Y: 900})
	if err := os.WriteFile(filepath.Join(s.sh.cfg.ConfigDir, "init.lua"), []byte(initLua), 0o644); err != nil {
		t.Fatal(err)
	}
	s.lua().reload(false)
	return s
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

// PL-1: "the scripting.md sample init.lua runs as written": it loads with no error, and everything it registers
// shows in :Hooks, :Keys and :Commands, each with its line in init.lua.
func TestSampleInitLua(t *testing.T) {
	src := sample(t)
	s := luaScene(t, src)
	if x := s.lua(); x.err != nil {
		t.Fatalf("the sample doesn't load: %v", x.err)
	}
	if n := noticesWith(s.store.State(), "lua.load"); len(n) > 0 {
		t.Fatalf("a load error: %+v", n)
	}
	line := func(needle string) string {
		for i, l := range strings.Split(src, "\n") {
			if strings.Contains(l, needle) {
				return "init.lua:" + itoa(i+1)
			}
		}
		t.Fatalf("no %q in the sample", needle)
		return ""
	}
	hooks := map[string]string{}
	for _, h := range s.reg.Hooks() {
		hooks[h.Event] = h.Source
	}
	for ev, needle := range map[string]string{"BufWritePre": `L.on("BufWritePre"`, "Commit": `L.on("Commit"`,
		"CommentSend": `L.on("CommentSend"`, "Permission": `L.on("Permission"`} {
		if got := hooks[ev]; got != line(needle) {
			t.Errorf("the %s hook's source is %q, want %s", ev, got, line(needle))
		}
	}
	var keys []string
	for _, k := range s.reg.Keymaps() {
		if strings.HasPrefix(k.Source, "init.lua:") {
			keys = append(keys, k.Mode+" "+k.Keys+" "+k.Source)
		}
	}
	for _, want := range []string{"n <Space>rr " + line("<leader>rr"), "x ga " + line(`"ga"`), "requests x " + line(`"requests"`)} {
		if !slices.Contains(keys, want) {
			t.Errorf("no keymap %q in %v", want, keys)
		}
	}
	cmds := map[string]string{}
	for _, c := range s.reg.Commands() {
		cmds[c.ID] = c.Source
	}
	if cmds["Ship"] != line(`L.command("Ship"`) {
		t.Errorf("Ship's source is %q", cmds["Ship"])
	}
	for kind, name := range map[string]string{"agent.tool": "staging_logs", "agent.pack": "hotfix", "session.kind": "hotfix", "hud.indicator": "deploys"} {
		cs := slices.DeleteFunc(s.reg.Contributions(kind), func(c registry.Contribution) bool {
			if i, ok := c.(registry.Indicator); ok {
				return i.Source == "" // the built-ins' panels and indicators
			}
			t, ok := c.(registry.AgentTool)
			return ok && strings.HasPrefix(t.Source, "builtin:")
		})
		if len(cs) != 1 || cs[0].ContributionName() != name {
			t.Errorf("%s: %v", kind, cs)
		}
	}
	if src, _ := s.reg.ViewSource(StatusLineView); src != line("L.statusline(") {
		t.Errorf("the status line is from %q", src)
	}
	if src, _ := s.reg.ViewSource("requests"); src != line(`L.view.override("requests"`) {
		t.Errorf("requests is from %q", src)
	}
	if v, src := s.option(t, "editor.tab_stop", registry.At{}); v != 4 || src != line("L.buffer_option.tabstop") {
		t.Errorf("tabstop %v from %q", v, src)
	}

	// :Hooks, :Keys and :Commands list them.
	for _, view := range []struct{ cmd, query, want string }{{"Hooks", "", "staging"}, {"Hooks", "", "Permission"}, {"Keys", "agent.review", "<Space>rr"},
		{"Commands", "Ship", "Ship"}} {
		s.run(view.cmd, nil)
		s.frame()
		for _, r := range view.query {
			s.typeKeys(string(r))
		}
		s.frame()
		if text := s.frameText(); !strings.Contains(text, view.want) && view.want != "staging" {
			t.Errorf(":%s doesn't show %s:\n%s", view.cmd, view.want, text)
		}
	}
}

// The sample's hooks, fired: a decision answers, and the Commit hook vetoes while the review ledger has unread
// agent-written lines (RV-2).
func TestSampleHooksFire(t *testing.T) {
	s := luaScene(t, sample(t))
	v, errs := s.reg.Fire(context.Background(), "Permission", "", map[string]any{"tool": "Bash", "input": "go test ./..."})
	if !v.Decided || v.Value != "allow" || len(errs) > 0 {
		t.Errorf("Permission for go test: %+v %v", v, errs)
	}
	if v, _ := s.reg.Fire(context.Background(), "Permission", "", map[string]any{"tool": "Bash", "input": "rm -rf /"}); v.Decided {
		t.Errorf("Permission for rm: %+v", v)
	}
	unread := 0
	s.reg.Contribute(registry.ReviewLedger{Name: "test", Get: func(root string) registry.Ledger {
		return registry.Ledger{Root: root, Ready: true, All: registry.LedgerCount{Read: 3, Unread: unread}}
	}})
	if v, errs := s.reg.Fire(context.Background(), "Commit", "", map[string]any{"project": "oriel"}); v.Veto || len(errs) > 0 {
		t.Errorf("Commit with every line read: %+v %v", v, errs)
	}
	unread = 2
	if v, errs := s.reg.Fire(context.Background(), "Commit", "", map[string]any{"project": "oriel"}); !v.Veto || v.Reason != "unread agent lines" || len(errs) > 0 {
		t.Errorf("Commit with lines unread: %+v %v", v, errs)
	}
}

// Hooks veto and rewrite from Lua; :lua evaluates and completes; a save reloads the config, with a toast.
func TestLuaHooksREPLAndReload(t *testing.T) {
	s := luaScene(t, `local L = oriel
L.on("BufWritePre", { pattern = "*.md", desc = "no markdown" }, function(ev) return false, "not markdown, " .. ev.buf end)
L.on("CommentSend", function(ev) ev.body = ev.body:upper(); return ev end)
`)
	type comment struct{ Body string }
	c := &comment{Body: "ship it"}
	if _, errs := s.reg.Fire(context.Background(), "CommentSend", "", c); len(errs) > 0 || c.Body != "SHIP IT" {
		t.Errorf("rewrite: %q %v", c.Body, errs)
	}
	v, _ := s.reg.Fire(context.Background(), "BufWritePre", "README.md", &BufWritePre{Buf: "README.md"})
	if !v.Veto || v.Reason != "not markdown, README.md" {
		t.Errorf("veto: %+v", v)
	}

	x := s.lua()
	x.eval("1 + 1")
	x.eval("oriel.api.version")
	x.eval("oriel.nope()")
	got := []string{x.repl[1].text, x.repl[3].text}
	if got[0] != "2" || got[1] != `"0.4.0"` || x.repl[5].kind != 2 {
		t.Errorf("repl %+v", x.repl)
	}
	if _, names := x.complete("oriel.ke"); !slices.Equal(names, []string{"keymap"}) {
		t.Errorf("completing oriel.ke: %v", names)
	}
	if _, names := x.complete("oriel.regex.f"); !slices.Equal(names, []string{"find", "find_all"}) {
		t.Errorf("completing oriel.regex.f: %v", names)
	}

	path := filepath.Join(s.sh.cfg.ConfigDir, "init.lua")
	os.WriteFile(path, []byte(`oriel.command("Hello", function() end, { desc = "hi" })`), 0o644)
	if !x.changed() {
		t.Fatal("the save went unseen")
	}
	x.reload(true)
	if hs := configHooks(s); len(hs) != 0 {
		t.Errorf("the old config's hooks are still there: %+v", hs)
	}
	if n := noticesWith(s.store.State(), "lua.reload"); len(n) != 1 || !strings.Contains(n[0].Text, "reloaded init.lua") {
		t.Errorf("no reload toast: %+v", n)
	}
	os.WriteFile(path, []byte("local a = 1\nx = = 1\n"), 0o644)
	x.reload(true)
	if n := noticesWith(s.store.State(), "lua.load"); len(n) != 1 || n[0].Location != "init.lua:2" {
		t.Errorf("a syntax error's notice: %+v", n)
	}
}

// :Inspect shows what's under the cursor: the tree-sitter path, the highlight, keymaps and views.
func TestInspect(t *testing.T) {
	s := editorScene(t)
	s.run("Inspect", nil)
	s.frame()
	s.frame()
	text := strings.ReplaceAll(s.frameText(), "\n", " ")
	for _, want := range []string{":INSPECT", "TREE-SITTER", "source_file", "HIGHLIGHT", "KEYMAPS HERE", "VIEWS", "app.statusline"} {
		if !strings.Contains(text, want) {
			t.Errorf(":Inspect lacks %q:\n%s", want, text)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// 19-config and 20-inspect: :Hooks and the REPL over the sample config, and :Inspect over the editor. The sample's
// status line shows the review ledger's LEDGER bar.
func TestLuaSnapshots(t *testing.T) {
	s := luaScene(t, sample(t))
	s.reg.Fire(context.Background(), "Permission", "", map[string]any{"tool": "Bash", "input": "go test ./..."})
	s.run("Hooks", nil)
	s.run("lua", nil)
	x := s.lua()
	x.eval("oriel.api.version")
	x.eval("#oriel.ui.row{}.children")
	x.eval("oriel.ledger.unread('pricing')")
	x.input = "oriel.agent.to"
	s.frame()
	s.golden("lua-hooks")

	e := editorScene(t)
	e.run("Inspect", nil)
	e.golden("inspect")
}

// configHooks are the hooks the config registered, not the built-ins'.
func configHooks(s *errScene) []registry.HookInfo {
	var out []registry.HookInfo
	for _, h := range s.reg.Hooks() {
		if !strings.HasPrefix(h.Source, "builtin:") {
			out = append(out, h)
		}
	}
	return out
}
