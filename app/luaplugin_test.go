package app_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/plugin/hosttest"
	"github.com/liamg/oriel/plugin/ossandbox"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/script/api"
)

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

// The capability guard rejects undeclared calls. A plugin's Lua with no grants gets ui (always allowed) and nothing
// else: each other door fails with an error naming the capability.
func TestPluginLuaGuard(t *testing.T) {
	s := apptest.LuaScene(t, "")
	g := plugin.NewGrants()
	p := plugin.Caller{ID: "acme/x", Dir: t.TempDir(), Guard: g}
	os.WriteFile(filepath.Join(p.Dir, "notes.txt"), []byte("own"), 0o644)
	vm := s.PluginVM(p)
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
		`oriel.agent.provider{ name = "x", program = "bin/x" }`:               `"agent.provider"`,
		`oriel.agent.bridge{ name = "x", program = "bin/x" }`:                 `"agent.provider"`,
		`oriel.lsp.server{ name = "x", languages = {"go"}, cmd = {"gopls"} }`: `"exec"`,
	} {
		err := luaErr(t, vm, src)
		if !strings.Contains(err, capability+" capability isn't declared") || !strings.Contains(err, "acme/x") {
			t.Errorf("%s: %q, want an error naming %s", src, err, capability)
		}
	}
	g.Grant("acme/x", []plugin.Grant{{Cap: plugin.CapAgentTools, Scope: []string{"lookup"}}})
	if err := luaErr(t, vm, `oriel.agent.tool{ name = "lookup", run = function() end }`); err != "" {
		t.Errorf("a declared tool: %s", err)
	}
	if err := luaErr(t, vm, `oriel.agent.tool{ name = "other", run = function() end }`); !strings.Contains(err, `for other`) {
		t.Errorf("a tool not declared: %s", err)
	}
}

// The gate capability covers veto / decide hooks. A plugin may observe an event, but its veto without gate is an
// error, not a veto.
func TestPluginGateHooks(t *testing.T) {
	s := apptest.LuaScene(t, "")
	g := plugin.NewGrants()
	vm := s.PluginVM(plugin.Caller{ID: "acme/gate", Guard: g})
	if err := luaErr(t, vm, `oriel.on("Commit", function(ev) seen = true; return false, "no" end)`); err != "" {
		t.Fatal(err)
	}
	v, errs := s.Registry().Fire(nil, "Commit", "", &struct{ Project string }{"p"})
	if v.Veto || len(errs) == 0 || !strings.Contains(errs[0].Error(), `"gate"`) {
		t.Errorf("a veto without gate: %+v, %v", v, errs)
	}
	if global(vm, "seen") != true {
		t.Error("the hook didn't run")
	}
	g.Grant("acme/gate", []plugin.Grant{{Cap: plugin.CapGate}})
	if v, _ := s.Registry().Fire(nil, "Commit", "", &struct{ Project string }{"p"}); !v.Veto {
		t.Error("with gate, no veto")
	}
}

// A plugin's WASM is started from its Lua with oriel.wasm.load, its own .wasm needing no grant.
func TestLuaWASM(t *testing.T) {
	s := apptest.LuaScene(t, "")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "m.wasm"), hosttest.Module(), 0o644)
	vm := s.PluginVM(plugin.Caller{ID: "acme/w", Dir: dir, Guard: plugin.NewGrants()})
	if err := luaErr(t, vm, `local m = oriel.wasm.load("m.wasm", { on = { ["fs.read"] = function(p) return "lua read " .. p.path end } })
sum = m:call("add", 2, 3)
echoed = m:call_json("echo", { result = { a = 1 } }).a
asked = m:call_json("ask", { path = "/x" })
n = #m:exports()`); err != "" {
		t.Fatal(err)
	}
	for name, want := range map[string]any{"sum": 5.0, "echoed": 1.0, "asked": "lua read /x", "n": 5.0} {
		if got := global(vm, name); got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	vm2 := s.PluginVM(plugin.Caller{ID: "acme/w2", Dir: dir, Guard: plugin.NewGrants()})
	if err := luaErr(t, vm2, `oriel.wasm.load("m.wasm"):call_json("ask", { path = "/etc/hostname" })`); !strings.Contains(err, `"fs.read" capability isn't declared`) {
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

// oriel.process.spawn drives a process plugin from Lua, in its OS sandbox.
func TestLuaProcess(t *testing.T) {
	if err := ossandbox.Available(); err != nil {
		t.Skip(err)
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip(err)
	}
	s := apptest.LuaScene(t, "")
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	os.WriteFile(filepath.Join(dir, "bin", "plugin"), []byte(rpcScript), 0o755)
	vm := s.PluginVM(plugin.Caller{ID: "acme/p", Dir: dir, Guard: plugin.NewGrants()})
	if err := luaErr(t, vm, `proc = oriel.process.spawn("bin/plugin", {
  on = { greet = function(p) greeted = p.n; return "hi" end, hello = function(p) hello = p end },
  on_exit = function(err) exited = true end,
})
proc:request("echo", { x = 1 }, function(res, err) answer = res; failed = err end)`); err != "" {
		t.Fatal(err)
	}
	s.Until("the answer", func() bool {
		return global(vm, "answer") != nil && global(vm, "hello") != nil && global(vm, "greeted") != nil
	})
	if a, h, g := global(vm, "answer"), global(vm, "hello"), global(vm, "greeted"); a != "pong" || h != "there" || g != 1.0 {
		t.Errorf("answer %v, hello %v, greeted %v", a, h, g)
	}
	if err := luaErr(t, vm, `assert(proc:running()); assert(proc:pid() > 0); proc:stop()`); err != "" {
		t.Fatal(err)
	}
	s.Until("the exit", func() bool { return global(vm, "exited") == true })
	found := false
	for _, r := range s.Logs.Ring.Records() {
		if r.Source == "plugin:acme/p" && r.Msg == "script up" {
			found = true
		}
	}
	if !found {
		t.Error("its stderr isn't in the log as plugin:acme/p")
	}
}

// A plugin registers options from Lua with OptionSpec, named plugin.<name>.<option>; they appear in the plugin's
// section of the settings page and save to [plugin.<name>]. settings.toml's value reaches the plugin, a name outside
// its own is refused, and the options go with the plugin.
func TestPluginOptions(t *testing.T) {
	s := apptest.LuaScene(t, "")
	rig.WriteFile(t, filepath.Join(s.ConfigDir, "settings.toml"), "[plugin.jira]\nsite = \"acme.atlassian.net\"\n")
	p := plugin.Caller{ID: "acme/jira", Dir: t.TempDir(), Guard: plugin.NewGrants()}
	vm := script.New()
	apis := api.InstallPlugin(vm, s.HostAs("builtin:lua"), p)
	defer vm.Close()
	if err := luaErr(t, vm, `oriel.option{ name = "plugin.jira.site", type = "string", default = "", help = "Your Jira host" }
oriel.option{ name = "plugin.jira.boards", type = "list", default = {} }
oriel.option{ name = "plugin.jira.mode", type = "enum", values = { "mine", "team" } }`); err != "" {
		t.Fatal(err)
	}
	settingsPlugin(s).Reload()
	if err := luaErr(t, vm, `site = oriel.option["plugin.jira.site"]; mode = oriel.option["plugin.jira.mode"]; boards = #oriel.option["plugin.jira.boards"]`); err != "" {
		t.Fatal(err)
	}
	for name, want := range map[string]any{"site": "acme.atlassian.net", "mode": "mine", "boards": 0.0} {
		if got := global(vm, name); got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	if sp, ok := s.Registry().Options().Spec("plugin.jira.site"); !ok || sp.Section != "jira" || sp.Help != "Your Jira host" {
		t.Errorf("the spec: %+v, %v", sp, ok)
	}
	if err := luaErr(t, vm, `oriel.option{ name = "editor.tabstop", type = "int", default = 8 }`); !strings.Contains(err, "plugin.jira.<option>") {
		t.Errorf("a plugin declaring another's option: %q", err)
	}
	if err := luaErr(t, vm, `oriel.option{ name = "plugin.jira.n", type = "int", default = "x" }`); err == "" {
		t.Error("a default of the wrong type was taken")
	}
	apis.Dispose()
	if _, ok := s.Registry().Options().Spec("plugin.jira.site"); ok {
		t.Error("the option outlived its plugin")
	}
}

// oriel.now is the window's clock, so a plugin's countdown follows a test's fixed time; oriel.platform says the OS.
func TestLuaNowAndPlatform(t *testing.T) {
	s := apptest.LuaScene(t, "")
	s.Now = time.Date(2026, 10, 15, 13, 12, 0, 500_000_000, time.UTC)
	if got := s.Lua("L.now()"); len(got) != 1 || got[0] != float64(s.Now.UnixMilli())/1000 {
		t.Errorf("L.now() = %v, want %v", got, float64(s.Now.UnixMilli())/1000)
	}
	if got := s.Lua("L.platform"); len(got) != 1 || got[0] != runtime.GOOS {
		t.Errorf("L.platform = %v", got)
	}
}

// oriel.exec_async runs in the background: the call returns at once, and fn gets the output, or why it failed, on
// the loop. oriel.exec's on_done, deprecated, is the same.
func TestLuaExecInBackground(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip(err)
	}
	s := apptest.LuaScene(t, "")
	if got := s.Lua(`L.exec_async({ "sh", "-c", "sleep 0.1; echo hi" }, {}, function(out, err) said, failed = out, err end)`); len(got) != 0 {
		t.Errorf("returned %v, want nothing", got)
	}
	if global(lua(s).VM(), "said") != nil {
		t.Fatal("on_done ran before the program finished")
	}
	s.Until("the output", func() bool { return global(lua(s).VM(), "said") != nil })
	if said, failed := global(lua(s).VM(), "said"), global(lua(s).VM(), "failed"); said != "hi\n" || failed != nil {
		t.Errorf("said %q, failed %v", said, failed)
	}
	s.Lua(`L.exec_async({ "/no/such/program" }, {}, function(out, err) missing = err end)`)
	s.Until("the missing program", func() bool { return global(lua(s).VM(), "missing") != nil })
	s.Lua(`L.exec({ "sh", "-c", "echo nope >&2; exit 3" }, { on_done = function(out, err) failed = err end })`)
	s.Until("the failure", func() bool { return global(lua(s).VM(), "failed") != nil })
	if f, _ := global(lua(s).VM(), "failed").(string); !strings.Contains(f, "exit status 3") || !strings.Contains(f, "nope") {
		t.Errorf("failed = %q", f)
	}
}

// A plugin's exec is checked against the program it runs: a relative one (./tool) is cwd's, both when its grant is
// checked and when it runs, so a grant for that file lets it run, and nothing else of the name does.
func TestLuaExecChecksTheProgramItRuns(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	tool := filepath.Join(dir, "tool")
	os.WriteFile(tool, []byte("#!/bin/sh\necho mine\n"), 0o755)
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "tool"), []byte("#!/bin/sh\necho other\n"), 0o755)
	s := apptest.LuaScene(t, "")
	g := plugin.NewGrants()
	g.Grant("acme/x", []plugin.Grant{{Cap: plugin.CapExec, Scope: []string{plugin.RealPath(tool)}}})
	vm := s.PluginVM(plugin.Caller{ID: "acme/x", Dir: t.TempDir(), Guard: g})
	if err := luaErr(t, vm, `said = oriel.exec({ "./tool" }, { cwd = "`+dir+`" })`); err != "" {
		t.Fatalf("the granted program: %s", err)
	}
	if got := global(vm, "said"); got != "mine\n" {
		t.Errorf("it printed %q", got)
	}
	if err := luaErr(t, vm, `oriel.exec({ "./tool" }, { cwd = "`+other+`" })`); !strings.Contains(err, "capability isn't declared") {
		t.Errorf("another directory's tool: %q, want it refused", err)
	}
}

// oriel.ui.ring draws widget.Ring: the label in the middle, read out with its name.
func TestLuaRing(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.hud.indicator("dial", { slot = "statusline.right" },
  oriel.ui.ring{ value = 0.3, label = "18m", name = "next meeting in", size = 20, color = "warn" })`)
	s.Frame()
	s.Frame()
	s.WantFrame("18m")
	if n := s.Node("next meeting in 18m"); n.Role != "status" {
		t.Errorf("the ring reads as %+v", n)
	}
}

// oriel.ui.spinner draws widget.Spinner, read out as what's in progress.
func TestLuaSpinner(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.hud.indicator("busy", { slot = "statusline.right" },
  oriel.ui.spinner{ name = "syncing the wiki", size = 12, color = "accent" })`)
	s.Frame()
	s.Frame()
	if n := s.Node("syncing the wiki"); n.Role != "status" {
		t.Errorf("the spinner reads as %+v", n)
	}
}

// oriel.ui.dots draws widget.Dots, read out as what's going on.
func TestLuaDots(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.hud.indicator("busy", { slot = "statusline.right" },
  oriel.ui.dots{ name = "the wiki bot is writing", size = 4, color = "accent" })`)
	s.Frame()
	s.Frame()
	if n := s.Node("the wiki bot is writing"); n.Role != "status" {
		t.Errorf("the dots read as %+v", n)
	}
}

// oriel.load and oriel.ui.async: the section shows its busy line while the work runs, then what body draws from the
// value done handed back; a failure shows Retry, which runs the work again.
func TestLuaLoadAsync(t *testing.T) {
	s := apptest.LuaScene(t, `finish = nil
runs = 0
wiki = oriel.load(function(done) runs = runs + 1; finish = done end)
oriel.statusline(function(w)
  return oriel.ui.async{ load = wiki, label = "Reading the wiki", retry = true,
    body = function(v) return oriel.ui.text(v .. " pages") end }
end)`)
	s.Frame()
	s.Until("the load to start", func() bool { return global(lua(s).VM(), "runs") == float64(1) })
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.Midway(func() { s.WantFrame("Reading the wiki…") })
	if n := s.Node("Reading the wiki…"); n == nil || n.Role != "status" {
		t.Errorf("the busy line reads as %+v", n)
	}
	s.Lua(`finish(nil, "the wiki is down")`)
	s.Now = s.Now.Add(time.Second) // past the loading line's minimum show
	s.Until("the failure", func() bool { return strings.Contains(s.Text(), "the wiki is down") })
	s.ClickNode("Retry")
	s.Until("the retry", func() bool { return global(lua(s).VM(), "runs") == float64(2) })
	s.Lua(`finish("12")`)
	s.Until("the value", func() bool { return strings.Contains(s.Text(), "12 pages") })
	st := s.Lua(`wiki:state().phase`)
	if len(st) != 1 || st[0] != "ready" {
		t.Errorf("h:state().phase = %v", st)
	}
}

// oriel.ui.busy, progress, skeleton and a busy button read out as what's in progress, and the busy button can't be
// pressed.
func TestLuaLoadingWidgets(t *testing.T) {
	s := apptest.LuaScene(t, `pressed = 0
oriel.statusline(function(w)
  return oriel.ui.row{
    oriel.ui.busy{ label = "Syncing the wiki" },
    oriel.ui.progress{ value = 0.5, label = "uploading" },
    oriel.ui.skeleton{ rows = 1, label = "pages" },
    oriel.ui.button{ label = "Sync", busy = true, busy_label = "Syncing", on_click = function() pressed = pressed + 1 end },
  }
end)`)
	s.Frame()
	s.Frame()
	for _, name := range []string{"Syncing the wiki…", "uploading 50%", "pages"} {
		if n := s.Node(name); n == nil || n.Role != "status" {
			t.Errorf("%q reads as %+v", name, n)
		}
	}
	s.WantFrame("Syncing…")
	s.Click(apptest.Centre(s.Node("Syncing…").Bounds), 0) // disabled, so not ClickOn
	s.Frame()
	if global(lua(s).VM(), "pressed") != float64(0) {
		t.Error("a busy button was pressed")
	}
}
