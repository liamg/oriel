# Scripting: the config language

The layer that makes it vim-levels customisable. It runs in-process, starts
in microseconds, and can touch everything.

## Which language

| Option | Pure Go | Speed | Familiar to vim users | Notes |
|---|---|---|---|---|
| **Lua 5.1 via gopher-lua** | yes | ok (≈ 3–5× slower than PUC Lua) | **yes (neovim)** | mature, coroutines |
| Lua via LuaJIT (cgo) | no | fastest | yes, same as neovim | cgo, harder cross-builds |
| JavaScript via goja | yes | ok | no | ES2020; npm culture with no npm |
| Go via Yaegi | yes | slow-ish | no | "Go everywhere", but weak sandboxing and big |
| Starlark | yes | ok | no | deterministic, no mutation: wrong for interactive |
| Tengo / Risor / Expr | yes | fast | no | small communities |
| Vimscript | – | – | yes | no |

**Recommendation: Lua**, behind a `script` interface.

- Start on **gopher-lua** (pure Go).
- Benchmark the hot paths (statusline at 60 Hz, keymap dispatch, decoration
  providers). If they miss budget, swap in LuaJIT via cgo behind the same interface.
- Lua is what neovim users already write, so the API shape below borrows
  neovim's where it's good, and a `vim` compatibility shim (stretch goal) can
  run simple neovim plugins: surround, comment, text-object packs.

## Lua and `settings.toml`

- Plain values live in `settings.toml`, which the settings page writes (`settings.md`). `init.lua` is for logic:
  keymaps, hooks, commands, and values computed at load.
- A value set in `init.lua` beats `settings.toml`, and the page shows it read-only with a link to the line.

## Execution model

- One VM on the UI loop, like neovim. Script calls are synchronous and never block.
- Async through coroutines plus `oriel.async` (timers, processes, RPC,
  agent calls), resumed on the UI loop.
- Heavy work belongs in a process extension. A watchdog warns, then
  interrupts, a script that runs past 50 ms.

## API shape (sketch)

```lua
local L = oriel

-- options
L.option.relativenumber = true
L.buffer_option.tabstop = 4                       -- buffer-local
L.session_option.agent = "claude"                  -- session-local

-- keymaps, any mode, including panels' own modes
L.keymap.set("n", "<leader>rr", "agent.review")             -- a command
L.keymap.set({ "n", "x" }, "ga", function() L.thread.new() end)
L.keymap.set("requests", "x", "request.resolve")            -- keys inside the Requests panel

-- commands
L.command("Ship", function(args)
  L.cmd("git.commit", { draft = true })
  L.cmd("pr.raise", { verify = true })
end, { desc = "commit, then verify & raise" })

-- events: observe / veto / rewrite / decide
L.on("BufWritePre", { pattern = "*.go" }, function(ev) L.lsp.format(ev.buf) end)

L.on("Commit", function(ev)
  if L.ledger.unread(ev.project) > 0 then
    return false, "unread agent lines"   -- veto
  end
end)

L.on("CommentSend", function(ev)        -- rewrite before the agent sees it
  ev.body = ev.body:gsub("@t(%d+)", function(n) return L.linear.link("INF-" .. n) end)
  return ev
end)

L.on("Permission", function(ev)         -- decide
  if ev.tool == "Bash" and ev.input:match("^go test ") then return "allow" end
end)

-- agent tools: offered to the session's agent over MCP
L.agent.tool{
  name = "staging_logs",
  desc = "Tail the staging logs for a service",
  params = { service = "string", lines = "number?" },
  run = function(p) return L.exec{ "kubectl", "logs", "-n", "staging", p.service, "--tail", p.lines or 200 } end,
}

-- prompt packs and session kinds
L.agent.pack("hotfix", { prompt = L.read("~/.config/oriel/agents/hotfix.md"), tools = { "staging_logs" } })
L.session.kind("hotfix", { pack = "hotfix", gates = { "tests", "ledger" }, status = "red-until-merged" })

-- views: statusline, HUD indicators, panels, all replaceable
L.statusline(function(w)
  return L.ui.row{
    L.ui.mode(),
    L.ui.text(w.session.branch, "dim"),
    L.ui.spacer(),
    L.ui.indicator{ label = "CTX", bar = w.session.ctx_frac },
    L.ui.indicator{ label = "LEDGER", bar = L.ledger.frac(), warn = 0.8 },
  }
end)

L.hud.indicator("deploys", { slot = "titlebar.right", every = "30s" }, function()
  local n = L.json(L.http.get("https://deploy.internal/api/pending")).count
  return L.ui.indicator{ label = "DEPLOY", value = n, tone = n > 0 and "warn" or "ok" }
end)

L.view.override("requests", function(s)            -- replace a built-in panel
  return L.ui.list(s.threads:filter("open"):sort("age"), require("my.thread_row"))
end)

-- decorations
L.decor.provider("coverage", function(buf, top, bot)
  return L.coverage.lines(buf, top, bot):map(function(l)
    return { line = l.n, sign = l.hit and "▎" or "", hl = l.hit and "CovHit" or "CovMiss" }
  end)
end)

-- status colours are rules too
L.status.rule(function(s) if s.kind == "hotfix" and not s.pr_merged then return "red" end end)
```

A plugin's own Lua also has these (catalog.md; P8, not built yet), which an `init.lua` doesn't:

```lua
local L = oriel
L.option{ name = "plugin.jira.site", type = "string", help = "Your Jira host", default = "" }
local icon = L.asset("icons/jira.svg")
local rpc = L.process.spawn("bin/oriel-jira")          -- JSON-RPC, OS-sandboxed
local fmt = L.wasm.load("wasm/fmt.wasm")
```

## Introspection

- `:Inspect` shows what's under the cursor: highlight groups, decorations,
  threads, the LSP token and the tree-sitter node.
- `:Keys`, `:Hooks`, `:Options`, `:Commands`, `:Views` and `:Plugins` are
  searchable, show sources, and jump to the definition.
- `:lua` REPL with completion over the whole `oriel.*` API. The API is typed
  through generated LuaLS annotations, so your config gets LSP completion too.

## Stability

- The API is generated from Go declarations: one source of truth for Lua,
  process RPC, docs and LuaLS types.
- `oriel.api.version`. Breaking changes need a major bump, and there's a
  deprecation window with runtime warnings.

## Go interfaces

```go
// package script — the VM is swappable (gopher-lua now, LuaJIT later)
type VM interface {
    Load(chunk Chunk) error                       // file or string, with a source name for errors
    Call(fn Ref, args ...Value) ([]Value, error)  // used by hooks, keymaps, views
    Bind(ns string, api any)                      // expose a Go value (generated binding table)
    Interrupt(reason string)                      // watchdog
    Stats() Stats                                 // per-function time and allocs, for :Hooks
}

type Value interface{ isValue() }                  // nil, bool, number, string, table, fn, userdata
```

```go
// bindings are generated from annotated Go, not hand-written
//
//oriel:api name=ledger
type LedgerAPI struct{ s *core.Store }

//oriel:api doc="Agent-written lines not yet read in a project."
func (l LedgerAPI) Unread(project string) int { return l.s.Ledger(project).Unread() }

// `go generate ./...` emits:
//   script/bind_ledger.go   — VM glue: argument checks, conversions
//   rpc/ledger.go           — the same method for process extensions
//   types/oriel.d.lua         — LuaLS annotations for completion in init.lua
//   docs/api/ledger.md
```

- One source of truth, so Lua, process RPC, WASM, the docs and the LuaLS types can't drift apart.
- A Lua-defined input engine goes through a small adapter that implements `input.Engine` and calls
  the VM per key. That's the path an emacs mode written in Lua would take (see `editor.md`).

## As built (PL-1)

- `script.VM` is gopher-lua (approved in deps.md): one state on the UI loop, Lua's standard libraries, `require`
  from `~/.config/oriel/lua`. The watchdog logs a call past 50 ms as slow and interrupts one past 500 ms (a config
  load gets 2 s); per-function times feed `:Hooks`. Errors are `script.Error`: the message, with the traceback kept
  apart (see "As built (PL-12)").
- The API is `script/api`: annotated Go (`//oriel:api name=…` on a namespace type, `//oriel:api doc=… optional=…
  returns=…` on its methods). `tools/cmd/apigen` (`go generate ./script/api`) writes the VM glue (`zz_bind.go`), the
  LuaLS types (`script/types/oriel.d.lua`) and the reference (`docs/api.md`); a test fails when they're stale. A
  parameter named `where` is the calling line, so everything registered says where it came from (`init.lua:12`).
- Namespaces now: `oriel.command`, `cmd`, `on`, `notify`, `log`, `read`, `exec`, `json`, `statusline`; `keymap`,
  `view`, `decor`, `agent` (tool, pack), `session` (kind), `hud` (indicator), `status` (rule), `thread`, `ui`, `regex` (PCRE2),
  `language`, and `option`, `buffer_option`, `session_option` over the registry's options (`oriel.option` sets the Lua layer;
  `local o = oriel.option` if you want it short). A list option takes a table of strings and a map option a table of
  strings by name (`oriel.option["sandbox.servers"] = { docs = "https://docs.example.com/mcp" }`); reading one gives
  the same. There's one global, `oriel`; a config that wants a short name writes `local L = oriel`, as the sample does.
- Registrations for later features (`agent.tool`, `agent.pack`, `session.kind`, `hud.indicator`, `status.rule`) are
  general registry contributions now (`Registry.Contribute`); their tickets consume them. `oriel.ledger`, `linear`,
  `http` and `async` load, and calling into one says which ticket brings it. An option
  set before anything registers it waits, and `:Hooks` lists it.
- Hooks are the registry's (`Registry.Hook`, `Fire`, `DeclareEvent`): in priority order, a veto or decision stops
  them, a rewrite changes the event the next sees; one that panics is turned off. The editor fires `BufRead` (a rewrite
  that sets `read_only`, saying why, opens the file read-only: how files outside the session do),
  `BufWritePre` (a veto stops the write) and `BufWritePost`; the sessions fire `CommentSend` (a rewrite changes the
  comment, a veto stops it), `Permission` and `AgentTurnEnd`; `Commit`, `PrRaise` and `ModeChanged` are declared for
  the features that fire them.
- Saving `init.lua` or anything under `lua/` reloads the config within 500 ms: everything it registered goes, it
  loads again, and a toast says how long and how many things it registered; an error is a notice with its line.
- `:Hooks` (19-config), `:lua` (the REPL: expressions first, `⇥` completion over `oriel`, history) and
  `:Inspect` (20-inspect) are built. `:Inspect`'s parts are contributions (`registry.InspectSection`): the editor
  gives the tree-sitter path (a new `node_path` in the grammar shim), the highlight and decorations; the Lua extension
  gives the keymaps that apply and the window's views, with what replaced them (`Registry.ViewSource`).
- Which-key's `?` opens the keymaps picker on the prefix.
- Not yet: the LuaJIT swap, process and WASM bindings of the same declarations, a `vim` shim, `:Plugins`, jumping
  from a source to its line (`gd`), and a project's `.oriel/init.lua` (it needs the trust prompt security.md
  describes).

## As built (PL-12)

- `script.Error` has `Where()` (a syntax error's line, the message's, or the innermost Lua frame's), `Frames()` and
  `Trace()`. `require` loads modules with their path from the config directory as the chunk name, so errors say
  `lua/my/tickets.lua:8`. In the traceback the API function that raised the error is named (`oriel.agent.tool`) and
  gopher-lua's own `[G]` frames are dropped; the Go stack under the API call (a panic in it is an error at its Lua
  call) comes after, behind "show Go frames" in the log. A hook's trace ends with `oriel.on: dispatch · <event>`.
- Lua drops a tail call's frame (`return f()`), as reference Lua does; the REPL evaluates `return unpack({ <line> })`
  so a call in a line keeps its frame.
- A reload loads the new config alongside the running one: if it fails, the new one's registrations and option
  values are undone (`APIs.Revert`) and the last good config keeps running, which the notice says. If it loads, the
  old one goes, and options it set that the new one doesn't are unset (`APIs.Handover`). At startup there's no good
  config to keep, so what ran before the error stays.
- Every Lua error with a line in the config is a diagnostic (owner `lua`) until the next load: load errors, and
  errors from hooks, views, decorations, keymaps and the REPL (`VM.OnError`). The load error's notice has
  **Edit init.lua** (the failing file's name); a successful load clears the diagnostics and the toast.
- A Lua view or decoration that errors reports it (`Registry.Report`) instead of panicking: a view draws its error
  in its place until the third in a row turns it off. Hooks are counted by `Fire`. The fault's notice is the row the
  errors were, "… · Permission hook turned off after 3 errors", with **Open at line** and **Turn back on**.
- `L.diagnostics.set(owner, file, list)` and `L.diagnostics.list()` bind the registry's diagnostics.
- Not yet: a column for runtime errors (gopher-lua gives none, so the whole line is underlined), and the toast's
  "saving reloads it" hint.
- UI from Lua: `L.ui.button{ label, on_click, primary, id }` runs Lua when clicked; `row` and `column` take `gap` and
  `pad` beside their children. A view whose top node is a single line (a row, text, a indicator, a button) is centred in
  the height it's given, so it sits right in a bar; a column runs from the top, as a panel does. A demo plugin
  (`app/plugindemo_test.go`) adds a dock panel with buttons, a tab, title-bar and status-line segments and editor
  marks, using only this API.
- `oriel.menu.add{ label, run, when, group, order, key }` adds a right-click entry (PL-13); `run` is a function, given
  what was clicked (`view`, `instance`, `file`, `selection`, `help`), or a command id.
- Form widgets (PL-14): `oriel.ui.input`, `toggle` and `select` on the same widgets the built-ins use
  (`widget.Input`, `widget.Toggle`, `widget.Select`; the settings page draws with the last two); `on_change` gets the
  new value and the view redraws. `oriel.ui.state(key, initial)` is a table the VM keeps by key (`VM.State`). A
  callback's error is a notice at its line. Tab moves focus: controls in the `button` key mode map Tab and ⇧Tab to
  `ui.focus.next`/`prev` (`UIView.FocusNext`), and a Lua input sets `InputOpts.TabMoves`.
- Drag and drop (PL-15): any `oriel.ui` node can be dragged (`drag = { kind, data, label }`) or take drops
  (`accepts = { kinds }`, `on_drop = function(kind, data)`); a `"file"` drop's data is its paths.

## As built (LS-1)

- `oriel.lsp.server{ name, languages, cmd, roots, settings, init, order }` registers a language server (a
  `registry.LSPServer`); one with a built-in's name replaces it.

## As built (AI-5)

- `oriel.thread`: `list()` (the session in front's requests: `{ id, title, status, file, line, busy, urgent, btw,
  comments, last, author }`), `new(opts)` (with `body`, sends a request about `file`:`line` or the whole change; without,
  opens a thread on the cursor's line), `reply(id, body, opts)` (`urgent`, `btw`), `resolve(id, status)` and
  `open(id)`. They run the `request.*` commands, so a keymap or a plugin does the same with `oriel.cmd`.

## As built (SH-17)

- `oriel.rail.add{ name, icon, title, command, args, scope, order, bottom }` adds an entry to the project sidebar
  (ui.md 01h). `scope = "project"` makes it one of each project's panels: it's listed under the open project and its
  command runs with `args.project` set to that project, so `command = "view.toggle", args = { id = "tickets" }` opens
  the `tickets` view for that project. `scope = "session"` (the default) lists it below the projects. A reload that
  drops the call removes the entry.
- A Lua view's `ctx.project` is the project its instance was opened for, "" for a session's view.

## As built (ED-15)

- `oriel.editor.action{ name, label, label_on, icon, icon_on, command, args, run, when, on, note, order, help }` adds a
  button at the right of an editor's breadcrumb (a `registry.EditorAction`): `when(path)` picks the files, `on(window)`
  lights it (`window` is `{ id, path, presenter }`), `label_on`/`icon_on` show while it's on, `note(window)` is dim text
  after its label; `command` runs with `args.window` set to the window it was clicked in, or `run` is a function.
- `oriel.editor.presenter{ name, label, when, comments, order, build = fn(ctx) }` adds a buffer presenter (a
  `registry.BufferPresenter`). `ctx` has `path`, `name`, `lines` and `width`; `ctx.block(from_line, to_line, fn)` adds
  a block made from those source lines (1-based), drawn by `fn(width)`, which returns `oriel.ui` nodes (or the nodes in
  place of `fn`). The file's threads sit in the margin beside the block holding their line, and with `comments` a click
  on a block opens a thread on its lines. `oriel.editor.present(name)` shows the focused window with it, and again
  shows its text (`editor.present`).

```lua
oriel.editor.presenter{ name = "csv", label = "Table", comments = true,
  when = function(path) return path:match("%.csv$") ~= nil end,
  build = function(ctx)
    for i, line in ipairs(ctx.lines) do
      ctx.block(i, i, function() return oriel.ui.text((line:gsub(",", "  │  "))) end)
    end
  end }
oriel.editor.action{ name = "csv.table", label = "Table", label_on = "Edit", command = "editor.present", args = { name = "csv" },
  when = function(path) return path:match("%.csv$") ~= nil end,
  on = function(window) return window.presenter == "csv" end }
```

## As built (AI-12)

- `oriel.ui.chart{ kind, series, labels, height, max, y_ticks, format, legend, hover, marks, bands, on_click, id, label }`
  draws a `widget.Chart`: `kind` is `bars` (stacked, the default), `area`, `line`, `spark` or `strip`; each series is
  `{ name, values, color, kind, dashed, from }`, `color` a theme token or `#rrggbb`; `format` (`money`, `percent`,
  `count`) formats the axis and the hover card; `on_click(series, point)` gets them counted from 1.
- `oriel.rail.add` takes `scope = "global"` (a page every session shares, below a second divider) and
  `note = fn(project)`, a live figure at the entry's right; `oriel.start.add` takes `link = true` (a small link under
  the actions) and `note = fn()`, its figure.
- The pages are commands, so `oriel.cmd("stats.open", { group = "session", repo = "api", range = "7d" })`,
  `oriel.cmd("stats.session", { id = … })` and `oriel.cmd("usage.open")` reach them; `oriel.option["stats.split_sessions"]`
  is the split.

