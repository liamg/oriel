# Architecture

Oriel is one Go module (`github.com/liamg/oriel`) and one process per machine: the window, the renderer, the state
store and the session host all run together. Sessions are objects inside the host, reached by plain Go calls; there
are no per-session servers and no sockets between the UI and the host. Agents, builds and tests run in a sandbox per
workspace (a container or macOS's seatbelt), behind a gateway MCP server and an egress proxy.

## Module layout

| Package | Owns |
|---|---|
| `text` | rope, snapshots, anchors, authorship, undo tree |
| `edit` | editing primitives every input engine drives (cursors, edits, registers, marks, motions); `edit/input` is the `input.Engine` interface |
| `vim` | the modal engine over `edit`; `vim/vimtest` runs `.vt` keystroke cases |
| `regex` | PCRE2-style and vim regex engines |
| `syntax` | tree-sitter grammars under wazero: highlights, folds, code units, outline |
| `jsonrpc`, `lsp`, `dap` | JSON-RPC framing and processes; the LSP and DAP clients |
| `testrun` | test discovery and runner adapters (go, pytest, vitest/jest, cargo), results, coverage |
| `term` | the terminal model behind `term.Emulator`; `term/ghostty` (libghostty-vt, wasm or native), `term/pty` |
| `core` | sessions, workspaces, threads, tasks and the typed events that change them: pure state and logic |
| `agent` | the `Provider` interface, its optional ones (`Featured`, `Sandboxed`, `LoginStarter`, `Logged`, `Discovered`) and transcript types; `agent/claude` (and `agent/claude/claudebox`, what it needs in a sandbox), `agent/codex`; `agent/rpcagent` (the Oriel agent protocol: any provider over JSON-RPC, for plugins), `agent/acp` (any Agent Client Protocol agent), `agent/mcptools` (a session's tools as the MCP server oriel); `agent/price` (a `Book` of models' list prices, read from cost.dev's model list, with the snapshot vendored in `spec/cost-dev` as its default: every cost estimate prices tokens with one, a provider with `StartSpec.Prices`); `agent/agenttest` (the conformance suite, a fake provider and one that plays the suite) |
| `host` | the session host: a facade over its parts (`host/worktree`, `host/agentrun`, `host/sandboxhost`, `host/github`, `host/repos`, `host/jobs`, `host/utility`, `host/aireview`, `host/persist`, `host/pricing`: the live price book, cost.dev's list fetched at start and daily and kept in `~/.oriel/prices.json`), each seeing the host only as a `host/hostcore` State |
| `sandbox` | sandbox backends, the gateway, egress, the static Linux helper the app embeds |
| `gitx`, `gh` | git and GitHub |
| `watch` | freshness managers: sources kept up to date only while something subscribes; `watch/gitsrc`, a work tree's git status as sources |
| `integ/*` | code hosts and trackers (GitLab, Azure, Linear) |
| `registry` | the contribution registry, pure: commands, keymaps, options, events and hooks, kinds, services, help; `registry/uireg` is what's drawn (views, decorations, pickers, the rail…); `registry/lang` holds language servers and debug adapters |
| `plugin` | the plugin host: manifests, process RPC, wasm, permissions, `plugin.Load` |
| `script` | the Lua VM and the `oriel.*` bindings (`script/api`, documented in [api.md](api.md)) |
| `catalog` | the plugin registry client and the plugin manager |
| `render` | display lists and the backend interface; `render/gio` (the window), `render/raster` (CPU drawing), `render/typeset`, `render/headless` and `render/snapshot` (tests) |
| `ui` | the immediate-mode toolkit: layout, input routing, focus, accessibility; `ui/widget` (every control), `ui/layout` (the window's tree of views as data) |
| `theme`, `toml` | theme files and token resolution; a round-tripping TOML 1.0 editor for settings and themes |
| `zstd` | a Zstandard (RFC 8878) decoder: whole buffers or streamed within a frame's window; Codex's compressed rollouts |
| `app` | the desktop app: `app/shell` is the window every feature lives in, features live in `app/<feature>`, and they share `app/kit` |
| `assistant`, `voice`, `update` | the assistant's conversation; dictation; signed self-update |
| `cmd/oriel` | the binary and its subcommands |
| `tools` | checks run by tests and CI: `layout`, `ratchet`, `depcheck`, `refcheck`, `leakcheck`, `framecheck`, `apigen`, `notices`, `perf` |

## Layering

`tools/layout` fails the build on any package that breaks these, through any chain of imports, tests included:

- `text`, `edit`, `vim`, `syntax`, `lsp`, `dap`, `jsonrpc`, `core`, `agent` and `term` never reach `ui` or `render`.
- `registry` never reaches `ui`, `render`, `lsp` or `dap`; `registry/uireg` may reach `ui` and `render` but not
  `lsp` or `dap`; `registry/lang` the reverse.
- `plugin` never reaches `ui/widget`.
- The host's parts never import each other, nor does `host/hostcore`; only the `host` facade wires them together.
  What one part needs of another, the facade hands it as a func.
- An `app/<feature>` package never imports another feature or the root `app`. Features share `app/kit`, meet through
  registry services, commands and events, and their tests drive the app through `app/apptest`.

Every seam that may be swapped is an interface in the consuming package, with implementations in subpackages.

## State and events

- The host is the source of truth for session state. It applies typed events to a `core.Store`, which the app shares.
- An event is a plain struct with `Scope()` and `Apply(*State) error`. The store applies it to a copy and keeps the
  copy only if it applied cleanly; an event that changes nothing is dropped.
- The UI reads the materialised `core.State` and never asks the host on a frame. Calls into the host run off the UI
  goroutine.
- Event logs are JSON lines; `core.Replay` reproduces a state byte for byte. Times are stored in UTC.
- Every exported host method, every goroutine it starts and every callback into the app recovers a panic, logs it
  and returns it as an error.

## Registry and services

Everything a feature adds goes through the registry: commands, keymaps, options, hooks, views, decorations, pickers,
session kinds, agent tools, help pages. Built-in features are plugins written against the same API a third-party
plugin uses, so each can be replaced, removed or configured from Lua.

What crosses features is a service, a command, a hook event or a core event, never another plugin's fields. A plugin
offers a typed service with `registry.Service{Name, Impl}` and others find it with `registry.ServiceOf[T]`; the
built-ins include `Editors`, `GitTrees`, `CodeSites`, `Ships`, `Sessions` and `host.Access`. A plugin reads and
changes state through its `plugin.Host` (`State`, `Apply`).

Plugins live as long as the app. Bringing a session in another directory to the front moves the window onto it
underneath them: the layout it leaves is saved, the pages over it close, the `DirChanged` hook fires, and the layout
saved for the new directory comes back, then the session's opening commands. Nothing a plugin offers (its agent
tools, hooks, subscriptions) goes away while it does, so a plugin keeps working for every session, not only the one
in front.

So a plugin asks where the window is when it needs to know, never once as it starts: `Host.Here()` is the session
and project in front, `kit.WindowDir` the directory, and the shell's `Parts.DirState` a state file kept for it; all
are safe off the loop. What it keeps per directory it keeps by directory (a language server per root, a directory's
breakpoints or saved terminal screens), so coming back finds it. One that must swap something as the window moves
does it on `DirChanged` (`kit.OnDirChanged`; `oriel.on("DirChanged", fn)` in Lua), which fires once the window is on
the new directory and before its layout comes back; a view opened from that hook is replaced by the layout, so open
one later. A project's own plugins (its settings' `[plugins]`) run while the window is on the project, starting and
stopping as it comes and goes.

What a plugin remembers per session across restarts (a panel's scope, that a kind's opening commands have run) it
keeps with `Host.Keep` and reads with `Host.Kept` (`oriel.session.keep` and `kept` in Lua): the window saves it in the
state directory until the session is gone.

## Freshness

Nothing polls or watches on its own. What a feature wants kept up to date is a source (`watch.Source`: files, a work
tree's git status in parts, a pull request, a stats query), and the feature subscribes to it with a `watch.Need`: how
stale a reading may be, and what it wants while the window is behind another app (pause, the default; slow; keep).
A manager (`watch.Manager`) holds one reading per source, watches its files or polls it at the tightest need among its
subscriptions, re-reads a source derived from others only when their readings change, and with no subscribers lets it
go. Each session that isn't archived has a manager, and the app has one for what's no session's; `app/watches` makes
them, hands them the window's focus, and offers them as the `watches` service, with the sources Lua names
(`oriel.subscribe`, `oriel.source`). A view subscribes while it's drawn (the git trees, the stats); a plugin that must
hear in the background (pull requests' approvals) asks to keep. The Performance page lists every source and who holds it.

## Plugins

| Tier | Runs as | Use it for |
|---|---|---|
| Config (`init.lua`) and Lua plugins | the in-process VM on the UI loop | keymaps, options, hooks, commands, views |
| Process plugins | a child process over JSON-RPC, OS-sandboxed | long-running work, integrations, other languages |
| WASM modules | wazero, in-process | grammars, pure transforms |
| Native Go | compiled in | the built-ins, and plugins linked with `oriel build --with` |

An installed plugin always enters through Lua, which starts its processes and wasm modules. Hooks can observe, veto,
rewrite or decide an event. Plugins are tested through the real app with `app/plugintest`. The Lua API is in
[api.md](api.md); [plugins.md](plugins.md) walks through writing one. A plugin brings an agent with a process speaking the Oriel agent protocol
([agent-protocol.md](agent-protocol.md)), or an Agent Client Protocol agent through the bundled ACP bridge.

## Sessions and kinds

A session is a workspace (plan docs plus a git worktree per repo, on the session's branch), a kind, an agent and
what accrues: threads, tasks, the review ledger, test runs, the transcript. Your own checkouts are never edited,
except by a `direct` session, which opens a directory in place.

A kind (work, plan, review, job, new repo, direct, or one a plugin adds) is a `registry.SessionKind` registered by the
plugin that owns it. Code asks the kind (`registry.Can`, `Label`, `Chips`, kind-scoped contributions) rather than
comparing `core.Kind*` values or reading another kind's data. A kind's own files say so with a `//oriel:kind <kind>`
line, and only they may name it.

## Agent providers

An agent provider (Claude, Codex, or a plugin's) is an `agent.Provider`, registered by its plugin as a
`registry.AgentProvider`: its name, title and square, and the provider. The built-ins are plugins too (`app/agents`),
so a plugin registering a provider under the same name replaces one. The sessions plugin gives the session host the
registry's providers (`host.SetProviders`); a session handed over when Oriel quit resumes once its provider is back.
A plugin's provider that isn't Go is an `rpcagent.Client`: the whole provider, sessions and all, over the Oriel agent
protocol ([agent-protocol.md](agent-protocol.md)) to the plugin's process, whose agent's processes run through the
session's own exec, in its sandbox when it has one.

What a provider can do past `agent.Session`'s least it says (`agent.Features`: a terminal, a tool-less fork, images,
a sandbox, signing in from Oriel, its notes file), and the app offers only that. Nothing outside a provider's own
package names it.

Claude runs as its interactive CLI in a PTY, with Oriel's mod for hooks, never `claude -p` (that path is billed per
token). Codex runs as `codex app-server` over stdio.

A sandbox knows no provider: each backend asks the session's provider what its agent needs where the backend puts it
(`agent.Sandboxed`: its binary for that platform, its config and sign-in in a directory of its own, the host
variables and endpoints it inherits, the hosts it reaches, the user's MCP servers for the gateway), and gives it that.
A sandboxed Claude gets the host's sign-in without its refresh token. While one runs, the provider hands every sandbox
the host's sign-in each minute, and near its end runs the host's own claude to renew it (`claudebox.Box.KeepLogins`).

## Rendering and the toolkit

- `render` takes display lists built each frame; `render/gio` draws them in the window and `render/raster` on the CPU,
  blending in sRGB, for tests.
- `ui` is immediate-mode: widgets are functions over a `*ui.Ctx`, their state keyed by id. Views are composed from
  `ui/widget`; a view never builds its own clickable. If no widget fits, add a general one with its theme tokens.
- Behaviour lives in the toolkit, so every screen gets it:
  - Floats sit on named layers (`ui.LayerPage`, `ui.LayerDialog`…), never numeric `Z`. One under `ui.LayerPane` is
    inlaid in its view and clipped as the view is, so it scrolls out with it.
  - A full-window page is `widget.Page`; a dialog is `widget.Scrim`. Every closable surface has Escape and a visible
    close. A page doesn't choose where it lies: it covers the frame's title bar and main area, beside the rail.
  - Nothing floating assumes the frame's sizes: the frame lays out its regions as `widget.Area`s (page, top, side,
    main, bottom), and a page, the HUD's drawer or a window asks `widget.AreaRect` where one is.
  - Focus has one owner, the toolkit. A view marks its default target and moves focus on purpose with
    `Ctx.RequestFocus`.
  - Text never runs out of its box: a row too narrow cuts its text with "…". Strips of tabs scroll sideways
    (`widget.Strip`); tables scroll (`ScrollX`); a chip that must stay whole is `Keep`.
  - Text selects and copies by default: a drag, a double click (a word) or a triple click (a node's text), then the
    platform's copy key. A selection stays in the nearest `Selectable` box, else the nearest that scrolls or floats,
    else the window. Controls, menus and popovers never select, and `NoSelect` opts a box out.
  - Loading looks the same everywhere: `plugin.Load` tracks a fetch, `widget.Async` and `widget.Busy` draw it.
  - Widgets get outside services with `ui.Provide` / `ui.Service`, never globals.
- Themes are TOML token tables (`theme`), resolved into a flat table the widgets read.

## Accessibility

`ui` builds an accessibility tree from widget ids each frame, and the app loop sends a window that implements
`render.Accessible` only the changes (`render.AccessDiff`). The window queues the screen reader's actions, and the
loop runs them through `UI.Act`. `render/gio` keeps a mirror of the tree that each update patches in place, and
connects it to the platform:

- **macOS:** Gio's NSView gets one `NSAccessibilityElement` per node, created when VoiceOver first reaches it and
  released when the node goes (cgo, `access_darwin.go`). Nothing is built until VoiceOver asks the view for its
  children. Diffs post focus, value, title, selection, layout and announcement notifications.
- **Linux:** an AT-SPI application over D-Bus (godbus, `access_atspi.go`) exports Accessible, Component, Action,
  Text, EditableText and Value objects under `/org/a11y/atspi/accessible`, and sends events from diffs. It joins the
  accessibility bus only while `org.a11y.Status.IsEnabled` is true.
- A frame that changes nothing accessible sends nothing. The role and state tables and the event lists are plain Go
  and tested on every platform.

## Testing

- Test-first. Behaviour tests cite a source: a `ref:` to a pinned, vendored spec under `spec/` and a verbatim
  `quote:`. `tools/refcheck` checks that each resolves and each quote is there.
- Where an oracle exists it decides: vim cases (`.vt` files) are checked against headless nvim (`make oracle`), git
  behaviour against the `git` binary.
- Stdlib `testing` only: table tests and golden files. Goldens are rewritten with `ORIEL_UPDATE=1`.
- UI tests run headless: `app/apptest` drives the real app a frame at a time on a fixed clock, clicking and typing
  as a user would; `render/snapshot` compares display lists and perceptual PNG diffs. The fixture workspace is held
  to the reference mockups in `testdata/mockups`.
- Every harness calls `internal/isolate.Test` first: its own TMPDIR, and a check that no goroutine outlives the test.
  Tests change a package's knob only through `internal/testhook.Set`. Tests run shuffled.
- Timing budgets are tests named `…Budget` using `tools/perf`; `make test` runs them alone, one package at a time.
- `tools/ratchet` counts what the code is moving away from; a count may only fall.
