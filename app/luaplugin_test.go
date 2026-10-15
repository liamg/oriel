package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/ext/exttest"
	"github.com/liamg/oriel/ext/ossandbox"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/script/api"
)

// pluginVM is a Lua VM for plugin p over the scene's host, as a plugin's init.lua runs in.
func (s *errScene) pluginVM(t *testing.T, p ext.Plugin) *script.VM {
	t.Helper()
	vm := script.New()
	apis := api.InstallPlugin(vm, s.lua().h, p)
	t.Cleanup(func() { apis.Dispose(); vm.Close() })
	return vm
}

func luaErr(t *testing.T, vm *script.VM, src string) string {
	t.Helper()
	err := vm.Load(script.Chunk{Name: "init.lua", Source: []byte(src)})
	if err == nil {
		return ""
	}
	return err.Error()
}

func global(vm *script.VM, name string) any {
	out, _ := vm.Eval(script.Chunk{Name: "t", Source: []byte("return " + name)})
	if len(out) == 0 {
		return nil
	}
	return out[0]
}

// PL-2 done when: "the capability guard rejects undeclared calls". A plugin's Lua with no grants gets ui (always
// allowed) and nothing else: each other door fails with an error naming the capability (security.md).
func TestPluginLuaGuard(t *testing.T) {
	s := luaScene(t, "")
	g := ext.NewGrants()
	p := ext.Plugin{ID: "acme/x", Dir: t.TempDir(), Guard: g}
	os.WriteFile(filepath.Join(p.Dir, "notes.txt"), []byte("own"), 0o644)
	vm := s.pluginVM(t, p)
	if err := luaErr(t, vm, `oriel.keymap.set("n", "<leader>x", "noop"); oriel.command("X", function() end)
own = oriel.read("notes.txt")`); err != "" {
		t.Fatalf("ui and its own files: %s", err)
	}
	if got := global(vm, "own"); got != "own" {
		t.Errorf("reading its own file: %v", got)
	}
	for src, capability := range map[string]string{
		`oriel.exec({ "true" })`:                                              `"exec"`,
		`oriel.process.spawn("true")`:                                         `"exec"`,
		`oriel.read("/etc/hostname")`:                                         `"fs.read"`,
		`oriel.wasm.load("/etc/hostname")`:                                    `"fs.read"`,
		`oriel.agent.tool{ name = "t", run = function() end }`:                `"agent.tools"`,
		`oriel.agent.pack("p", { prompt = "x" })`:                             `"agent.prompt"`,
		`oriel.thread.list()`:                                                 `"agent.read"`,
		`oriel.session.kind("k", {})`:                                         `"session.control"`,
		`oriel.lsp.server{ name = "x", languages = {"go"}, cmd = {"gopls"} }`: `"exec"`,
	} {
		err := luaErr(t, vm, src)
		if !strings.Contains(err, capability+" capability isn't declared") || !strings.Contains(err, "acme/x") {
			t.Errorf("%s: %q, want an error naming %s", src, err, capability)
		}
	}
	g.Grant("acme/x", []ext.Grant{{Cap: ext.CapAgentTools, Scope: []string{"lookup"}}})
	if err := luaErr(t, vm, `oriel.agent.tool{ name = "lookup", run = function() end }`); err != "" {
		t.Errorf("a declared tool: %s", err)
	}
	if err := luaErr(t, vm, `oriel.agent.tool{ name = "other", run = function() end }`); !strings.Contains(err, `for other`) {
		t.Errorf("a tool not declared: %s", err)
	}
}

// security.md: gate covers "veto / decide hooks". A plugin may observe an event, but its veto without gate is an
// error, not a veto.
func TestPluginGateHooks(t *testing.T) {
	s := luaScene(t, "")
	g := ext.NewGrants()
	vm := s.pluginVM(t, ext.Plugin{ID: "acme/gate", Guard: g})
	if err := luaErr(t, vm, `oriel.on("Commit", function(ev) seen = true; return false, "no" end)`); err != "" {
		t.Fatal(err)
	}
	v, errs := s.reg.Fire(nil, "Commit", "", &struct{ Project string }{"p"})
	if v.Veto || len(errs) == 0 || !strings.Contains(errs[0].Error(), `"gate"`) {
		t.Errorf("a veto without gate: %+v, %v", v, errs)
	}
	if global(vm, "seen") != true {
		t.Error("the hook didn't run")
	}
	g.Grant("acme/gate", []ext.Grant{{Cap: ext.CapGate}})
	if v, _ := s.reg.Fire(nil, "Commit", "", &struct{ Project string }{"p"}); !v.Veto {
		t.Error("with gate, no veto")
	}
}

// plugins.md: a plugin's WASM is started from its Lua with oriel.wasm.load, its own .wasm needing no grant.
func TestLuaWASM(t *testing.T) {
	s := luaScene(t, "")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "m.wasm"), exttest.Module(), 0o644)
	vm := s.pluginVM(t, ext.Plugin{ID: "acme/w", Dir: dir, Guard: ext.NewGrants()})
	if err := luaErr(t, vm, `local m = oriel.wasm.load("m.wasm", { on = { ["fs.read"] = function(p) return "lua read " .. p.path end } })
sum = m:call("add", 2, 3)
echoed = m:request("echo", { result = { a = 1 } }).a
asked = m:request("ask", { path = "/x" })
n = #m:exports()`); err != "" {
		t.Fatal(err)
	}
	for name, want := range map[string]any{"sum": 5.0, "echoed": 1.0, "asked": "lua read /x", "n": 5.0} {
		if got := global(vm, name); got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	vm2 := s.pluginVM(t, ext.Plugin{ID: "acme/w2", Dir: dir, Guard: ext.NewGrants()})
	if err := luaErr(t, vm2, `oriel.wasm.load("m.wasm"):request("ask", { path = "/etc/hostname" })`); !strings.Contains(err, `"fs.read" capability isn't declared`) {
		t.Errorf("the module's undeclared host call: %s", err)
	}
}

// A process plugin, a shell script speaking JSON-RPC a line at a time: it asks the plugin's Lua (on.greet),
// notifies it (on.hello), answers its requests, and logs to stderr.
const rpcScript = `#!/bin/sh
echo "script up" >&2
echo '{"jsonrpc":"2.0","id":"h1","method":"greet","params":{"n":1}}'
echo '{"jsonrpc":"2.0","method":"hello","params":"there"}'
while read -r l; do
  case "$l" in
  *'"method":"echo"'*)
    id=${l#*\"id\":}; id=${id%%,*}
    echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":\"pong\"}" ;;
  esac
done
`

// PL-2: oriel.process.spawn drives a process plugin from Lua, in its OS sandbox.
func TestLuaProcess(t *testing.T) {
	if err := ossandbox.Available(); err != nil {
		t.Skip(err)
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip(err)
	}
	s := luaScene(t, "")
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	os.WriteFile(filepath.Join(dir, "bin", "plugin"), []byte(rpcScript), 0o755)
	vm := s.pluginVM(t, ext.Plugin{ID: "acme/p", Dir: dir, Guard: ext.NewGrants()})
	if err := luaErr(t, vm, `proc = oriel.process.spawn("bin/plugin", {
  on = { greet = function(p) greeted = p.n; return "hi" end, hello = function(p) hello = p end },
  on_exit = function(err) exited = true end,
})
proc:request("echo", { x = 1 }, function(res, err) answer = res; failed = err end)`); err != "" {
		t.Fatal(err)
	}
	s.until(t, "the answer", func() bool {
		return global(vm, "answer") != nil && global(vm, "hello") != nil && global(vm, "greeted") != nil
	})
	if a, h, g := global(vm, "answer"), global(vm, "hello"), global(vm, "greeted"); a != "pong" || h != "there" || g != 1.0 {
		t.Errorf("answer %v, hello %v, greeted %v", a, h, g)
	}
	if err := luaErr(t, vm, `assert(proc:running()); assert(proc:pid() > 0); proc:stop()`); err != "" {
		t.Fatal(err)
	}
	s.until(t, "the exit", func() bool { return global(vm, "exited") == true })
	found := false
	for _, r := range s.sh.cfg.Logs.Ring.Records() {
		if r.Source == "plugin:acme/p" && r.Msg == "script up" {
			found = true
		}
	}
	if !found {
		t.Error("its stderr isn't in the log as plugin:acme/p")
	}
}

// catalog.md, "Plugin options": "A plugin registers options from Lua with OptionSpec, named plugin.<name>.<option>
// … They appear in the plugin's section of the settings page, save to [plugin.jira]". settings.toml's value reaches
// the plugin, a name outside its own is refused, and the options go with the plugin.
func TestPluginOptions(t *testing.T) {
	s := luaScene(t, "")
	writeFile(t, filepath.Join(s.sh.cfg.ConfigDir, "settings.toml"), "[plugin.jira]\nsite = \"acme.atlassian.net\"\n")
	p := ext.Plugin{ID: "acme/jira", Dir: t.TempDir(), Guard: ext.NewGrants()}
	vm := script.New()
	apis := api.InstallPlugin(vm, s.lua().h, p)
	defer vm.Close()
	if err := luaErr(t, vm, `oriel.option{ name = "plugin.jira.site", type = "string", default = "", help = "Your Jira host" }
oriel.option{ name = "plugin.jira.boards", type = "list", default = {} }
oriel.option{ name = "plugin.jira.mode", type = "enum", values = { "mine", "team" } }`); err != "" {
		t.Fatal(err)
	}
	s.settings().loadAll()
	if err := luaErr(t, vm, `site = oriel.option["plugin.jira.site"]; mode = oriel.option["plugin.jira.mode"]; boards = #oriel.option["plugin.jira.boards"]`); err != "" {
		t.Fatal(err)
	}
	for name, want := range map[string]any{"site": "acme.atlassian.net", "mode": "mine", "boards": 0.0} {
		if got := global(vm, name); got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	if sp, ok := s.reg.Options().Spec("plugin.jira.site"); !ok || sp.Section != "jira" || sp.Help != "Your Jira host" {
		t.Errorf("the spec: %+v, %v", sp, ok)
	}
	if err := luaErr(t, vm, `oriel.option{ name = "editor.tabstop", type = "int", default = 8 }`); !strings.Contains(err, "plugin.jira.<option>") {
		t.Errorf("a plugin declaring another's option: %q", err)
	}
	if err := luaErr(t, vm, `oriel.option{ name = "plugin.jira.n", type = "int", default = "x" }`); err == "" {
		t.Error("a default of the wrong type was taken")
	}
	apis.Dispose()
	if _, ok := s.reg.Options().Spec("plugin.jira.site"); ok {
		t.Error("the option outlived its plugin")
	}
}

// oriel.now is the window's clock, so a plugin's countdown follows a test's fixed time; oriel.platform says the OS.
func TestLuaNowAndPlatform(t *testing.T) {
	s := luaScene(t, "")
	s.now = time.Date(2026, 10, 15, 13, 12, 0, 500_000_000, time.UTC)
	if got := s.eval(t, "L.now()"); len(got) != 1 || got[0] != float64(s.now.UnixMilli())/1000 {
		t.Errorf("L.now() = %v, want %v", got, float64(s.now.UnixMilli())/1000)
	}
	if got := s.eval(t, "L.platform"); len(got) != 1 || got[0] != runtime.GOOS {
		t.Errorf("L.platform = %v", got)
	}
}

// oriel.exec with on_done runs in the background: the call returns at once, and on_done gets the output, or why
// it failed, on the loop.
func TestLuaExecInBackground(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip(err)
	}
	s := luaScene(t, "")
	if got := s.eval(t, `L.exec({ "sh", "-c", "sleep 0.1; echo hi" }, { on_done = function(out, err) said, failed = out, err end })`); len(got) != 1 || got[0] != nil {
		t.Errorf("returned %v, want nothing", got)
	}
	if global(s.lua().vm, "said") != nil {
		t.Fatal("on_done ran before the program finished")
	}
	s.until(t, "the output", func() bool { return global(s.lua().vm, "said") != nil })
	if said, failed := global(s.lua().vm, "said"), global(s.lua().vm, "failed"); said != "hi\n" || failed != nil {
		t.Errorf("said %q, failed %v", said, failed)
	}
	s.eval(t, `L.exec({ "sh", "-c", "echo nope >&2; exit 3" }, { on_done = function(out, err) failed = err end })`)
	s.until(t, "the failure", func() bool { return global(s.lua().vm, "failed") != nil })
	if f, _ := global(s.lua().vm, "failed").(string); !strings.Contains(f, "exit status 3") || !strings.Contains(f, "nope") {
		t.Errorf("failed = %q", f)
	}
}

// oriel.ui.ring draws widget.Ring: the label in the middle, read out with its name.
func TestLuaRing(t *testing.T) {
	s := luaScene(t, `oriel.hud.indicator("dial", { slot = "statusline.right" },
  oriel.ui.ring{ value = 0.3, label = "18m", name = "next meeting in", size = 20, color = "warn" })`)
	s.frame()
	s.frame()
	s.wantFrame(t, "18m")
	if n := s.access("next meeting in 18m"); n.Role != "status" {
		t.Errorf("the ring reads as %+v", n)
	}
}
