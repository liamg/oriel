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
L.o.relativenumber = true
L.bo.tabstop = 4                       -- buffer-local
L.so.agent = "claude"                  -- session-local

-- keymaps, any mode, including panels' own modes
L.keymap.set("n", "<leader>rr", "agent.review")             -- a command
L.keymap.set({ "n", "x" }, "ga", function() L.thread.new() end)
L.keymap.set("requests", "x", "thread.resolve")             -- keys inside the Requests panel

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

-- views: statusline, HUD tats, panels, all replaceable
L.statusline(function(w)
  return L.ui.row{
    L.ui.mode(),
    L.ui.text(w.session.branch, "dim"),
    L.ui.spacer(),
    L.ui.tat{ label = "CTX", bar = w.session.ctx_frac },
    L.ui.tat{ label = "LEDGER", bar = L.ledger.frac(), warn = 0.8 },
  }
end)

L.hud.tat("deploys", { slot = "titlebar.right", every = "30s" }, function()
  local n = L.json(L.http.get("https://deploy.internal/api/pending")).count
  return L.ui.tat{ label = "DEPLOY", value = n, tone = n > 0 and "warn" or "ok" }
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
