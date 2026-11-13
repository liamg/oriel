# Write your first plugin

This walks through a small plugin from an empty directory to one that's installed, tested and debugged. It's in
Lua, the tier every plugin enters through ([architecture.md](architecture.md), "Plugins"). The full API is in
[api.md](api.md); the in-app help has more under `plugins/` (`:help plugins/lua`).

The plugin we'll build, `me/saves`, remembers the files you save in each session. It adds:

- an option, for what the status line calls the count;
- a hook on `BufWritePost`, which records each save;
- a status line indicator with the count;
- a panel listing the files, opened from the sidebar, a command or a key;
- the list kept per session, across switches and restarts;
- a command that posts the count to Slack, through a secret it never sees.

## What a plugin is

A plugin is a directory:

```
saves/
  oriel.toml   the manifest: who it is and what it may do
  init.lua     its entry point, run when it loads
  assets/      optional read-only files (oriel.asset)
  bin/         optional process binaries
```

`oriel plugin init --id me/saves saves` writes a starting `oriel.toml` and `init.lua` (`--process` adds a Go command
under `cmd/`).

## How long it runs

A plugin starts once and runs until Oriel quits, or until it's turned off or updated. Switching sessions doesn't start
it again: the window moves onto the new session's directory underneath it, and its hooks, tools and views go on
working for every session, not only the one in front. So don't note where you are once at the top of `init.lua`: read
it when you need it. A view gets the session in front as `ctx.session` and the project it's for as `ctx.project`; a
hook's event names its session; `oriel.session.kept("", key)` is the session in front's.

When the window moves, the `DirChanged` hook fires with `{ dir, was, session }`, once it's on the new directory and
before that directory's layout comes back (a view opened from the hook is replaced by it, so open one later). Use it
to swap something you keep per directory. The new directory's layout waits, behind the opening cover, until the
built-ins have read what they keep for it (its settings, layout and saved terminals), so it comes back whole. A
plugin listed in a project's own settings (`[plugins]` in `.oriel/settings.toml`) runs only while the window is on
that project.

## The manifest

```toml
id          = "me/saves"
version     = "0.1.0"
oriel       = ">=0.6"
description = "The files you've saved in each session"
license     = "MIT"

[permissions]
"session.control" = { why = "Keep each session's list of saved files with the session" }
```

- `id` is `<owner>/<name>`. Your options are named after the name: `plugin.saves.…`.
- `version` is semver; `oriel` is the Oriel versions it needs.
- `[permissions]` lists the capabilities it needs, each with a `why` that the install screen shows as yours. A
  capability Oriel doesn't know is an error, so a typo grants nothing.

Other top-level fields: `icon`, `runs` (`app`, `session` or `both`), `binaries` (the `os/arch` pairs it ships a
process for), `lazy` (load on first use, below), and a `[contributes]` table for files it brings with no Lua
(`themes`, `fonts`, `icons`, `languages`, `prompts`, `help`).

### Capabilities

`ui`, `theme` and `notify` are always allowed. Everything else is declared, and each API call in [api.md](api.md)
says which it needs. Some take a scope:

| Capability | Scope |
|---|---|
| `fs.read`, `fs.write` | `paths = [...]` (`worktree`, `config`, or explicit paths) |
| `net` | `hosts = [...]` |
| `secrets` | `NAME = "host"`, one per secret |
| `exec` | `commands = [...]` |
| `agent.tools` | `tools = [...]` |

The rest take only `why`: `editor.read`, `editor.write`, `input.observe`, `agent.read`, `agent.prompt`,
`agent.provider`, `gate`, `session.control`, `git`, `clipboard`. The install help page
(`plugins/install`) gives each one's risk.

Installing is all or nothing: the user grants every capability listed, or none. Anything not declared is refused when
the plugin tries it, with an error naming the capability.

## Step 1: an option

`init.lua` runs in the `oriel` Lua VM. Start with an option:

```lua
local L = oriel

local KEY = "saves.files"

L.option{ name = "plugin.saves.label", type = "string", default = "saves",
  help = "What the status line calls the count",
  on_change = function() L.hud.refresh("saves") end }
```

A plugin's options must be named `plugin.<name>.…`. They show in the plugin's section of the settings page and are
saved under `[plugin.saves]` in `settings.toml`. Read one with `L.option["plugin.saves.label"]`.

## Step 2: keep state per session

`oriel.session.keep(id, key, value)` keeps a value for a session, and `oriel.session.kept(id, key)` reads it back. An
empty `id` is the session in front. The value crosses as JSON and lasts across session switches and restarts, until
the session is gone. Put your plugin's name in the key. Both need `session.control`.

```lua
local function saved()
  return L.session.kept("", KEY) or {}
end
```

For state a view keeps across redraws (a draft in a field, say), use `oriel.ui.state(key, initial)` instead.

## Step 3: a hook

`oriel.on(event, opts?, fn)` hooks an event. `fn(ev)` gets the event as a table, its fields in snake case:

```lua
L.on("BufWritePost", { desc = "Remember the file" }, function(ev)
  local files = saved()
  table.insert(files, 1, ev.file)
  L.session.keep("", KEY, files)
  L.hud.refresh("saves")
end)
```

`opts.pattern` is a glob over the event's file (`{ pattern = "*.go" }`), `opts.priority` orders hooks, and `desc` is
what `:Hooks` shows. Returning nothing observes. Returning `false, reason` vetoes, a changed `ev` rewrites, any other
value decides; vetoing or deciding needs the `gate` capability. Other events include `BufRead`, `BufWritePre`,
`Commit`, `CommentSend` and `Permission` (see the sample `init.lua` in [configuration.md](configuration.md)).

Hooks run on the UI loop: keep them quick. One that panics, or errors 3 times in a row, is turned off.

## Step 4: a status line indicator

```lua
L.hud.indicator("saves", { slot = "statusline.right" }, function()
  return L.ui.text(L.option["plugin.saves.label"] .. " " .. #saved(), "dim")
end)
```

The slot is one of `titlebar.right`, `statusline.left`, `statusline.right`, `hud` or `hud.today`. The
build function returns `oriel.ui` nodes, and runs again every `opts.every`, on each event in `opts.on`, and on
`oriel.hud.refresh(name)`, which the hook and the option above call.

## Step 5: a panel

`oriel.view.override(id, fn)` registers a view. `fn(ctx)` returns `oriel.ui` nodes:

```lua
L.view.override("saves", function()
  local files = saved()
  if #files == 0 then
    return L.ui.text("No saves yet", "dim")
  end
  return L.ui.column{
    L.ui.list(files, function(f) return L.ui.text(f) end),
    L.ui.button{ id = "saves.clear", label = "Clear", on_click = function() L.run("saves.clear") end },
    gap = 8,
  }
end)
```

The nodes are `row`, `column`, `text`, `list`, `button`, `input`, `toggle`, `select`, `indicator`, `ring`, `chart`
and more; [api.md](api.md) under `oriel.ui` lists them all.

## Step 6: commands, a key and a sidebar entry

```lua
local panel = { id = "saves", place = "dock", edge = "left", title = "Saves" }

L.command("saves.clear", function()
  L.session.keep("", KEY, nil)
  L.hud.refresh("saves")
end, { desc = "Forget this session's saves" })

L.command("Saves", function() L.run("view.toggle", panel) end, { desc = "Show or hide this session's saves" })

L.keymap.set("n", "<leader>S", "Saves", { desc = "Saves" })

L.rail.add{ name = "saves", icon = "◆", title = "Saves", command = "view.toggle", args = panel, order = 60 }
```

- `oriel.command(name, fn, opts)` adds a command: `:Saves` and the omnibox run it. `fn` gets its arguments.
- `oriel.run(id, args)` runs any command, here the built-in `view.toggle`.
- `oriel.keymap.set(modes, lhs, rhs, opts)` maps keys to a command id or a Lua function, in `n`, `i`, `x` or a
  panel's own mode.
- `oriel.rail.add` adds an entry to the sidebar. `icon` is a built-in icon's name or a glyph. `scope` is `session`
  (the default), `project` or `global`.

Other places to add things: `oriel.start.add` (the start view), `oriel.menu.add` (right-click), `oriel.editor.action`
(the editor's breadcrumb), `oriel.picker.source` (the omnibox), `oriel.inbox.add` (an inbox card).

## Step 7: secrets and HTTP

A plugin never reads a secret. It names one, and Oriel attaches it to requests bound for that secret's host, over
https. Declare the host and the secret:

```toml
[permissions]
"session.control" = { why = "Keep each session's list of saved files with the session" }
net               = { hosts = ["hooks.slack.com"], why = "Post your count to Slack" }
secrets           = { SLACK_WEBHOOK = "hooks.slack.com", why = "Your Slack incoming webhook, sent only to hooks.slack.com" }
```

Then make the request. A webhook is a secret that's a whole URL, so it goes as `url_secret`; a token goes as `auth`:

```lua
L.command("saves.post", function()
  L.http.request({ method = "POST", url_secret = "SLACK_WEBHOOK",
    headers = { ["Content-Type"] = "application/json" },
    body = L.json_encode({ text = #saved() .. " files saved" }) }, function(res, err)
    if err then
      return L.notify("Slack: " .. tostring(err), "error")
    end
    if res.status >= 400 then
      return L.notify("Slack: HTTP " .. res.status, "error")
    end
    L.notify("Posted to Slack")
  end)
end, { desc = "Post this session's save count to Slack" })

L.start.add{ name = "saves.post", icon = "◆", title = "Post saves to Slack", command = "saves.post",
  shown = function() return L.secret.has("SLACK_WEBHOOK") end }
```

The user sets the value with `:Secret SLACK_WEBHOOK`, or in **Settings › Secrets**, which lists every secret your
plugin declares with your `why`. `oriel.secret.has` says whether it's set, never what it is: here the start view's
entry stays hidden until it is. Until then, a request fails saying which `:Secret` to run.

The request runs off the loop and calls back on it. For a token, `auth = "NAME"` sends it as a bearer token, or with
`auth_scheme` (`"token"`, `"basic"`, `"none"`). [examples/gitea](examples/gitea) is a whole code host built this way.

## Installing it

List the directory under `[plugins]` in `~/.config/oriel/settings.toml`:

```toml
[plugins]
"me/saves" = { path = "~/src/saves" }
```

Adding the line installs it (`plugins.sync` from the omnibox does it now). Oriel shows the capabilities it asks for,
with your reasons, and loads it once you accept. What you accepted is recorded in `oriel-lock.toml`; an update that
asks for more asks again. A `path` source is marked **unverified**: nobody has reviewed it.

`:Plugins` lists it with what it contributes and the capabilities it holds.

### Loading on first use

A plugin can wait to load until it's first used, with `lazy` in its manifest:

```toml
lazy = { commands = ["Saves", "saves.*"], events = ["BufWritePost"] }
```

It also takes `keys`, `filetypes`, `omnibox` (prefixes) and `views` (globs over view ids). `oriel.placeholder` does
the same from Lua.

## Processes and WASM

Lua runs on the UI loop. For long-running work, other languages or pure computation, `init.lua` starts something
else:

- `oriel.process.spawn("bin/<name>", opts)` starts the plugin's own binary, speaking JSON-RPC 2.0 over stdio, a
  message a line. It runs in an OS sandbox holding only the plugin's grants; its data directory is in
  `$ORIEL_PLUGIN_DATA`. `proc:request(method, params, fn)` calls it. A program that isn't the plugin's own needs
  `exec`. List the platforms you ship for in `binaries`. `oriel plugin init --process` starts one in Go.
- `oriel.wasm.load("x.wasm", opts)` loads a module into wazero, with no way out but the host functions it imports
  from `oriel` (`request`, `notify`, `log`), each checked against the plugin's grants.
- An agent comes as `oriel.agent.provider`, served by a process over the Oriel agent protocol
  ([agent-protocol.md](agent-protocol.md)) or by the bundled ACP bridge: [examples/acp](examples/acp) is four
  lines of Lua.

## Testing it

`app/plugintest` drives a plugin through the real app, with no window: it draws frames on a clock the test sets,
loads the plugin from its directory under its manifest's grants, and lets you run commands, click, type and read the
frame.

```go
package saves_test

import (
	"context"
	"testing"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/plugintest"
	"github.com/liamg/oriel/core"
)

func TestMain(m *testing.M) { plugintest.Main(m) }

func TestSaves(t *testing.T) {
	a := plugintest.New(t, plugintest.Options{})
	a.Apply(core.SessionSynced{Session: core.SessionState{ID: "s1", Name: "s1", Kind: core.KindWork}},
		core.SessionFocused{ID: "s1"})
	a.Load("plugin", nil) // the directory with oriel.toml; nil grants what it declares

	a.Run("Saves", nil)
	a.UntilText("No saves yet")

	a.Registry().Fire(context.Background(), "BufWritePost", "/src/main.go", kit.BufWritePost{File: "/src/main.go"})
	a.UntilText("/src/main.go", "saves 1")

	a.SetOption("plugin.saves.label", "written")
	a.UntilText("written 1")

	a.ClickText("Clear")
	a.UntilText("No saves yet", "written 0")
}
```

- `New` starts the app with a home of the test's; `Options` sets the window `Size`, the clock's start (`Now`) and
  the app's `Config` (`Net` and `Secrets`, for HTTP against a fake server). `SetNow` moves the clock.
- `Apply` applies core events, as the session host would: here a session, in front.
- `Run` runs a command; `SetOption` sets an option as `settings.toml` would.
- `UntilText`, `WantText`, `Has` and `Find` (the accessibility tree) check the frame; `ClickText`, `Type` and
  `TypeIn` drive it.
- A plugin with a process: `plugintest.Install(t, src, "./cmd/<name>")` lays it out as an install does, with the Go
  command built into `bin/`. `Tool` and `CallTool` call its agent tools.

`app/plugintest/plugintest_test.go` drives its `testdata/hello` plugin through all of these.

## Inspecting and debugging

- An error in `init.lua` is a notice naming the file and line, with the Lua traceback, and a row in `:Problems`.
- `:lua` opens a REPL in the same VM; `:lua <code>` runs one line. `oriel.api.version` shows the API's version.
- `oriel.log(msg, level)` writes to the log; `:Log` opens it, `:Log level debug` shows debug records.
  `oriel.notify(msg, level)` tells the user.
- `:Hooks` lists every hook with its event, who registered it and its p99 time. A hook over the 2 ms budget is
  marked, and a warning once it's over on two calls running; one turned off shows its last error, with **Turn back
  on**.
- `:Inspect` shows what's under the cursor: decorations and who added them, the keymaps that apply, and the views
  that make up the window.
- `:Plugins` shows what each plugin contributes, the capabilities it holds and how it runs.
- Use `oriel.now()` for any time you show, not `os.time`, so a test can fix it.
- `script/types/oriel.d.lua` has LuaLS annotations for the whole API: point your editor's Lua language server at it.

## Where next

- [api.md](api.md): every `oriel.*` function, with the capability it needs.
- [examples/gitea](examples/gitea): a code host in plain Lua, over HTTP with a secret.
- [examples/acp](examples/acp): an agent through the ACP bridge.
- [configuration.md](configuration.md): options, keymaps, commands and hooks in your own `init.lua`.
- The in-app help: `plugins/install`, `plugins/lua`, `plugins/hooks`, `plugins/secrets`, `plugins/inspect`.
- `oriel plugin pack` builds release archives; `oriel plugin publish` proposes a version to the registry.
