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
- Heavy work belongs in a process plugin. A watchdog warns, then
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
L.keymap.set("n", ",", "my.thing", { nowait = true })       -- don't wait for ",x" mappings (vim's <nowait>)

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
  ev.body = ev.body:gsub("@t(%d+)", function(n) return L.linear.link("ENG-" .. n) end)
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
L.agent.explainer{ name = "local", order = 5, label = "a local model",   -- answers every "explain this" first
  byline = "written by a local model",                                  -- under a long answer: a walkthrough
  run = function(req) return my_model(req.subject .. "\n" .. req.context, req.depth, req.format) end }
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

A plugin's own Lua also has these (catalog.md; `asset` is PL-19's, `process` and `wasm` PL-2's and `option` PL-10's, all
open to `init.lua` too):

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
//   rpc/ledger.go           — the same method for process plugins
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
  `language`, `ship` (gate: a "before this ships" gate, FG-6), and `option`, `buffer_option`, `session_option` over the registry's options (`oriel.option` sets the Lua layer;
  `local o = oriel.option` if you want it short). A list option takes a table of strings and a map option a table of
  strings by name (`oriel.option["sandbox.servers"] = { docs = "https://docs.example.com/mcp" }`); reading one gives
  the same. There's one global, `oriel`; a config that wants a short name writes `local L = oriel`, as the sample does.
- PL-4: `oriel.agent.tool{…}` also takes `for` (the agents it's offered to: session kinds, or `"assistant"`; none
  is every session's agent), `read_only` and `reads` (the data it reads, which the assistant lists as a source):
  `oriel.agent.tool{ name = "deploys", ["for"] = { "assistant" }, read_only = true, reads = "deploys", run = … }`.
- Registrations for later features (`agent.tool`, `agent.pack`, `session.kind`, `hud.indicator`, `status.rule`) are
  general registry contributions now (`Registry.Contribute`); their tickets consume them. `linear`, `http` and
  `async` load, and calling into one says which ticket brings it. RV-2 built `oriel.ledger`: `unread(project)` (the
  agent-written lines not read, unread or only skimmed) and `frac(project)` (the share read, 1 with none), project
  being the session's name for it or a path, the project in front when omitted. An option
  set before anything registers it waits, and `:Hooks` lists it.
- Hooks are the registry's (`Registry.Hook`, `Fire`, `DeclareEvent`): in priority order, a veto or decision stops
  them, a rewrite changes the event the next sees; one that panics is turned off. The editor fires `BufRead` (a rewrite
  that sets `read_only`, saying why, opens the file read-only: how files outside the session do),
  `BufWritePre` (a veto stops the write) and `BufWritePost`; the sessions fire `CommentSend` (a rewrite changes the
  comment, a veto stops it), `Permission` and `AgentTurnEnd`; `git.commit` fires `Commit` (RV-2: `{ project, root, message, files,
  unread }`, after the commit gates, a veto stops it); `PrRaise` and `ModeChanged` are declared for the features
  that fire them.
- Saving `init.lua` or anything under `lua/` reloads the config within 500 ms: everything it registered goes, it
  loads again, and a toast says how long and how many things it registered; an error is a notice with its line.
- `:Hooks` (19-config), `:lua` (the REPL: expressions first, `⇥` completion over `oriel`, history) and
  `:Inspect` (20-inspect) are built. `:Inspect`'s parts are contributions (`registry.InspectSection`): the editor
  gives the tree-sitter path (a new `node_path` in the grammar shim), the highlight and decorations; the Lua plugin
  gives the keymaps that apply and the window's views, with what replaced them (`Registry.ViewSource`).
- Which-key's `?` opens the keymaps picker on the prefix.
- Not yet: the LuaJIT swap, process and WASM bindings of the same declarations, a `vim` shim, `:Plugins`, jumping
  from a source to its line (`gd`), and a project's `.oriel/init.lua` (it needs the trust prompt security.md
  describes).

## As built (SH-9)

- `oriel.inbox.add(card)` adds a card to the inbox, or replaces the one with its `id`: `{ id, type, title, sub, group,
  mine, session, icon, tone, kicker, detail, code, note, interrupt, banner, done, expires (seconds from now), actions =
  { { label, key, primary, command, args } } }`. An action runs its command and answers the card (it goes to the
  inbox's history); `oriel.inbox.remove(id)` takes one away. A VM's cards are its own inbox source, `lua:<plugin>`,
  dropped on reload. Capability `ui`.

## As built (SH-8)

- `oriel.hud.indicator(name, opts, build)`: `opts { slot, order, every, on = { event names }, title, note, span, help }`;
  `build` is a function returning `oriel.ui` nodes, or the nodes themselves. It's built again every `every`, on each
  core event named in `on`, and by `oriel.hud.set(name, nodes)` (shown until it's next built; for good, for one given
  its nodes) or `oriel.hud.refresh(name)` (`hud.refresh`). `slot = "hud"` makes it a HUD panel, `"mission"` (SH-7) a part of mission control's strip, `"hud.today"` a line
  under Today. `oriel.ui.indicator` takes `dot` and `id` and is drawn by `widget.Indicator`.
- The sample's `every = "30s"` is a duration as Go reads it.

## As built (PL-10)

- `oriel.option{ name, type, default, help, values, min, max, step, follows, section }` declares an option: type is bool,
  int, float, string, enum (its first value the default), duration, key, list or map. `step` gives a ranged number a
  slider on the settings page; `follows` names an option whose value it takes until it's set. A plugin's must be named
  `plugin.<name>.<option>` (its id's name) and go in its own section of the settings page; the config may name any.
  They're disposed with what declared them. `oriel.option[...]` still reads and sets them; `{}` is an empty list.
- `oriel.now()` is the window's clock in seconds (a test's fixed time), `oriel.platform` the OS (`darwin`, `linux`).
- `oriel.exec(argv, { on_done = fn(output, err) })` runs the program in the background and returns nothing.
- `oriel.ui.ring{ value, label, color, size, name, id }` is a dial (`widget.Ring`); `oriel.ui.text(s, "title")` is a
  heading. `oriel.ui.spinner{ name, color, size, id }` says something is in progress (`widget.Spinner`).

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
- `oriel.menu.add{ label, run, args, when, group, order, key }` adds a right-click entry (PL-13); `run` is a function,
  given what was clicked (`view`, `instance`, `project`, `file`, `selection`, `help`), or a command id run with `args`. An entry
  that runs `agent.fix` (with `kind`, `subject`, `context`, `file`, `line`, `fix`) gets an "Ask the agent to explain"
  twin beside it (TR-6).
- Form widgets (PL-14): `oriel.ui.input`, `toggle` and `select` on the same widgets the built-ins use
  (`widget.Input`, `widget.Toggle`, `widget.Select`; the settings page draws with the last two); `on_change` gets the
  new value and the view redraws. `oriel.ui.state(key, initial)` is a table the VM keeps by key (`VM.State`). A
  callback's error is a notice at its line. Tab moves focus: controls in the `button` key mode map Tab and ⇧Tab to
  `ui.focus.next`/`prev` (`UIView.FocusNext`), and a Lua input sets `InputOpts.TabMoves`. `lines = n` makes an input
  take several lines (`InputOpts.Lines`, SE-5): ⏎ a new line, ⌘⏎ submits.
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
- `oriel.editor.viewer{ name, view, when = fn(path), order, help }` adds a file viewer (a `registry.FileViewer`):
  `editor.open` opens a file `when(path)` takes as a tab of `view`, its instance the file's absolute path, in place of a
  buffer. The first viewer by `order`, then name, that takes a file shows it.

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
  `oriel.cmd("stats.origin", { origin = "outside" })`, `oriel.cmd("stats.session", { id = … })` and `oriel.cmd("usage.open")` reach them; `oriel.option["stats.split_sessions"]`
  is the split.


## As built (AI-14)

- `oriel.ui.date_picker{ from, to, on_change, placeholder, id, label }` draws a `widget.DateSelect`: a button showing
  the range that opens a `widget.DatePicker` under it. `from` and `to` are days as `YYYY-MM-DD`, and
  `on_change(from, to)` gets them so.
- `oriel.cmd("stats.custom", { range = "2026-09-01..2026-09-30" })` sets the Stats page's custom range; with no
  range it opens the date picker.


## As built (SE-6–8)

- Repos are commands: `oriel.cmd("repos.clone", { url = "https://github.com/acme/billing", parent = "~/code" })`,
  `oriel.cmd("repos.remove", { repo = "billing" })`, `oriel.cmd("repos.housekeep", { repo = "oriel", apply = {
  "liam/old" }, prune = true })`, `oriel.cmd("repos.tag", { repo = "oriel", name = "v0.4.3", push = true })` (`pull`,
  `sign`, `push` and the message default to the `repos.tag.*` options). A command returns no value, so the facts and
  the housekeeping plan stay on the Repos page.

## As built (SE-4)

- Jobs are commands too: `oriel.cmd("jobs.open", { job = "sweep-stale-deps" })`, `oriel.cmd("jobs.run", { name = … })` (a job's key is its name),
  and `oriel.cmd("jobs.save", { title = "Weekly changelog draft", prompt = "…", sandbox = "sandbox", provider =
  "claude", model = "…", effort = "high", schedule = "weekly mon 07:00", run = false })` saves one
  without the dialog: given a `name`, it changes only what's given. `schedule` is `"manual"`, `"weekdays 07:00"`,
  `"weekly mon 07:00"`, `"mon,thu 07:00"`, or a table `{ every = "days", days = { "Mon", "Thu" }, at = "07:00" }`.
  `jobs.new` opens the dialog with any of them filled in; `jobs.delete { name }` forgets one; `session.delete { id }`
  throws a run (or any session of Oriel's) away.


## As built (PL-2)

- `oriel.process.spawn(path, { args, cwd, env, on = { method = fn(params) }, on_exit = fn(err) })` starts a process
  plugin and returns `proc`: `proc:request(method, params, fn(result, err))` (the answer comes on the loop),
  `proc:notify(method, params)`, `proc:on(method, fn)`, `proc:pid()`, `proc:running()`, `proc:stop()`. The
  process's requests and notifications reach `on` first, then the host methods (plugins.md, "As built (PL-2)").
- `oriel.wasm.load(path, { on })` loads a module and returns `mod`: `mod:call(fn, ...)` with numbers,
  `mod:request(fn, value)` with JSON, `mod:exports()`, `mod:close()`. A call is stopped past the watchdog's budget.
- A plugin's VM is `api.InstallPlugin(vm, host, plugin.Caller{…})`; your config's is `api.Install(vm, host, dir)`,
  trusted. A relative path given to `oriel.read`, `process.spawn` or `wasm.load` is in the plugin's directory (your
  config's for `init.lua`). The config's processes and modules stop when it reloads.
- Every API function declares its capability: `//oriel:api name=keymap cap=ui` on a namespace, or `cap=…` on a
  function, and the glue apigen writes checks it before the call; `cap=target` is a function that checks its own
  against what it's given (`read`, `process.spawn`, `wasm.load`). One with neither fails `go generate`.
  docs/api.md says each function's. Besides the glue: `exec` checks its program, `lsp.server` its `cmd`,
  `agent.tool` its name, and a hook's veto or decision needs `gate` (without it the hook's run is an error and
  nothing is vetoed).

## As built (PL-19)

- `oriel.asset(path)` returns the contents of one of the plugin's own assets, as a string: path is under its
  `assets/` directory and may not lead out of it, and it needs no capability (catalog.md, "As built (PL-19)").
- `api.Reference` lists each function's capability as `Cap`, which `oriel plugin pack` checks plugins against.

## As built (PL-6)

- Updates are commands: `oriel.cmd("update.check")` checks now and opens the card, `update.install` installs the
  release it found, `update.restart` restarts onto it.
  `oriel.option["updates.mode"] = "manual"` stops the hourly check; `oriel.option["updates.channel"] = "nightly"`
  takes pre-releases.

## As built (PL-11)

- Prompts are commands: `oriel.cmd("prompts.set", { id = "style.code", text = "Tabs, never spaces." })` keeps your
  version (empty text or the default puts it back), `prompts.reset { id }`, `prompts.open { id }`,
  `prompts.edit { id }`. Ids are `style.code`, `pack.<kind>` and `part.<part>`.
- Voice is commands and options: `oriel.cmd("voice.dictate")` (`⌃⇧Space`), `voice.speak { text = "Done." }`,
  `voice.download { model = "small.en" }`; `oriel.option["voice.wake"] = true`. `oriel.on("VoiceWake", fn)`
  sees what followed the wake word: return a decision to take the request yourself.

## As built (PL-9)

- `oriel.http.request(spec, fn(res, err))` makes a request off the loop and answers on it: `spec { method, url,
  headers, body, auth, url_secret, timeout }`, `res { status, body, headers }`. The host must be in the plugin's `net`
  grant; `auth` and `url_secret` name a secret the host attaches for the host its grant binds it to, which the plugin
  never reads (security.md, "As built (PL-9)"). The sample's blocking `oriel.http.get` isn't built: it still says it
  comes with AI-7.
- `oriel.ship.action{ name, label = "text" | fn(pr), command, args, run = fn(pr), order, help }` adds a button to a
  raised PR (plugins.md, "As built (PL-9)"); `pr` is `{ session, project, repo, number, title, url, state, draft,
  merged }`, and a label function returning nil leaves it off.


## As built (PL-8)

- `oriel.picker.source{ id, title, prefix, order, picker_only, items = fn(query), run = fn(item), preview = fn(item) }`
  adds an omnibox and picker source (ui.md 02b's `oriel.picker.source`): items are tables `{ label, detail, note, icon,
  icon_style, badge, badge_style, key, boost, id, … }`, matched by the omnibox on label and detail, and handed back
  to `run` and `preview` as given. `items` runs as the omnibox draws, so answer from what you have and fetch in the
  background.
- `oriel.ticket.source{…}` was the launcher's lookup alone; it's `oriel.ticket.tracker` now ("As built (tickets)").
- `oriel.http.request` takes `auth_scheme`: `"none"` sends the secret as it is (Linear), anything else before it, and
  `Bearer` when it isn't given.
- `oriel.on("SessionStart", fn)` and `oriel.on("PrSeen", fn)` see a session start and its PR first read (plugins.md,
  "As built (PL-8)").

## As built (tickets)

- `oriel.ticket.tracker{ name, title, order, keys, secret, lookup, assigned, start, review, done, link }` (cap
  `agent.prompt`) makes a ticket tracker, and Oriel builds the rest on it: the omnibox's `!` tickets, the launcher's
  card, Continue for a ticket with a session, the moves as a session starts from a ticket and its PR is seen and
  merged, the PR link, the ask for its key and the assistant's `my_tickets` (plugins.md, "As built (tickets)"). `keys` is a regular expression (Go's) the tracker's keys
  match, `secret` one the plugin declares. Each function answers `done(value)` or `done(nil, why)`, now or later:
  `lookup = fn(key, done)` with a ticket, `assigned = fn(done)` with a list of them, `start`, `review` and `done =
  fn(key, done)` with `{ moved, state }` (`done` is asked once the session's PRs are merged; its callback is the second
  argument like the others', so name it as you like), `link = fn(key, pr, done)` given `pr = { number, url, repo,
  title }`. A ticket is `{ key,
  title, url, state, stage, project, priority, priority_label, blocked, blocked_by, pr = { number, url, repo }, summary,
  facts, context }`; `stage` is `todo`, `started` or `review`.
- `oriel.on("PrMerged", fn)` sees a session's PR merged, whoever merged it: `{ session, project, ticket, number, url,
  title, open }`, `open` the session's other PRs still open (plugins.md, "As built (tickets)").


## As built (PL-5)

- `oriel.inbox.add` takes `body` (oriel.ui nodes, or a function returning them, drawn under the card's text: a field
  to type an answer in) and an action's `run = fn()` instead of a command; a card's `expires` counts from the
  window's clock.
- `oriel.agent.fork({ session, prompt, timeout }, fn(answer, err, label))` (cap `agent.prompt`) asks a session's
  agent a side question, the first forker's answer: the session in front by default, else the newest open one.
- `oriel.agent.tool` takes `confirm = fn(p)` (an act: it returns `{ title, lead, rows = { { key, value, url } }, note,
  line }`, called on the loop, and run's table `{ result, line, detail }` says what was done), `async = true` (run
  gets `done(result, err)` and is called on the loop) and `adds = "tool"` (a part of that tool's answer).
- `oriel.option{ …, on_change = fn(value) }` hears the option change, from wherever it's set.
- `oriel.hud.indicator` takes `slot = "inbox.foot"` (the Inbox panel's foot) and, since SH-7, `"mission"`.


## As built (reviews)

- `oriel.code.host{ name, title, icon, order, words, takes, remotes, secret, requested = fn(done), lookup = fn(url,
  done), checkout = fn(url, done), … }` (cap `ui`) makes a code host (plugins.md, "As built (code hosts)"): Oriel
  builds the Reviews page, the pill, the notices, the omnibox rows and the assistant's `review_requests` on its review
  functions. `takes` is a regular expression its PRs' URLs match. A request is `{ url, repo, number, title, author,
  draft, asked, via, state, since, adds, dels, files, ci }` (`asked` RFC 3339 or seconds); a detail `{ request, base,
  head, mergeable, labels, reviewers = { { login, state } }, body, files = { { path, adds, dels } }, checks, clone }`;
  `checkout` answers `{ remote, ref }`.
- `oriel.rail.add` takes `badge = fn()` (a count shown as a pill, 0 for none) and `badge_tone` (a theme token).

## As built (code hosts)

- `oriel.code.host` also takes `words = { short, long, sigil }`, `sites = { { host, api, secret } }`, `path` (a
  regular expression over a remote's path, its first group the repo's name), `takes` (over a PR URL's path), `shape`
  (over a whole remote), `setup = { api, secret, token_url, token_help, probe = fn(host, done) }`, `tokens = { env,
  hosted_env, command, optional }` (where a site's token is found before the user is asked: env vars must be secrets
  the manifest declares, the command needs `exec`); its review
  functions get the site first (`requested(site, done)`, `lookup(site, url, done)`, `checkout(site, url, done)`), and
  Ship's functions, each given `repo = { session, project, remote, repo, site }` and
  answering through `done`: `for_branch(repo, branch)`, `create(repo, { title, body, branch, base, draft })`,
  `edit(repo, pr, title, body)`, `ready(repo, pr)`, `request_reviewers(repo, pr, logins)`, `checks(repo, pr)`,
  `merge_methods(repo, pr)` (`{ allowed, default }`), `blockers(repo, pr)` (`{ { kind, count, text, hard, url } }`),
  `merge(repo, pr, { method, head, title, body, delete_branch })`, and for review mirroring `threads(repo, pr)`
  (`{ { id, path, line, old, outdated, resolved, reviewer, state, url, comments = { { id, author, body, at, member } } } }`,
  no id for the conversation) and `reply(repo, pr, thread, body)` (`thread = { id, path, line, old, url }`; the new
  comment's id). Answers cross as JSON, where an empty table is an empty list or object. A Lua error in a later
  callback (an `http.request`'s) fails the call awaiting it. `docs/examples/gitea` is a worked host. A pull request is `{ number, id, title, body, url,
  author, state, draft, merged, branch, base, head, reviewers }`. Values cross as their JSON. It replaces
  `oriel.review.source`.
- `oriel.http.request`'s `auth_scheme = "basic"` sends the secret as HTTP Basic's password.

## As built (tickets page)

- `oriel.rail.add` and `oriel.start.add` take `shown`: `fn(project)` and `fn()`, asked as each frame is built; the
  entry is listed only while it returns true (plugins.md, "As built (tickets page)").
- `oriel.secret.has(name)` says whether a secret the plugin declares is set: a bool, never its value. With `shown`, it
  keeps a plugin's entries hidden until its key is set: `shown = function() return oriel.secret.has("LINEAR_TOKEN") end`.
- `oriel.ticket.tracker` takes `icon`, an icon's name drawn beside its tickets where more than one tracker's are listed.
- An `icon` may name one a plugin's `[contributes] icons` brings (a raster image), as well as the built-in set.
