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
| `agent` | the `Provider` interface and transcript types; `agent/claude`, `agent/codex`; `agent/agenttest` (the conformance suite and a fake provider) |
| `host` | the session host: a facade over its parts (`host/worktree`, `host/agentrun`, `host/sandboxhost`, `host/github`, `host/repos`, `host/jobs`, `host/utility`, `host/aireview`, `host/persist`), each seeing the host only as a `host/hostcore` State |
| `sandbox` | sandbox backends, the gateway, egress, the static Linux helper the app embeds |
| `gitx`, `gh` | git and GitHub |
| `integ/*` | code hosts and trackers (GitLab, Azure, Linear) |
| `registry` | the contribution registry, pure: commands, keymaps, options, events and hooks, kinds, services, help; `registry/uireg` is what's drawn (views, decorations, pickers, the rail…); `registry/lang` holds language servers and debug adapters |
| `plugin` | the plugin host: manifests, process RPC, wasm, permissions, `plugin.Load` |
| `script` | the Lua VM and the `oriel.*` bindings (`script/api`, documented in [api.md](api.md)) |
| `catalog` | the plugin registry client and the plugin manager |
| `render` | display lists and the backend interface; `render/gio` (the window), `render/raster` (CPU drawing), `render/typeset`, `render/headless` and `render/snapshot` (tests) |
| `ui` | the immediate-mode toolkit: layout, input routing, focus, accessibility; `ui/widget` (every control), `ui/layout` (the window's tree of views as data) |
| `theme`, `toml` | theme files and token resolution; a round-tripping TOML 1.0 editor for settings and themes |
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

## Plugins

| Tier | Runs as | Use it for |
|---|---|---|
| Config (`init.lua`) and Lua plugins | the in-process VM on the UI loop | keymaps, options, hooks, commands, views |
| Process plugins | a child process over JSON-RPC, OS-sandboxed | long-running work, integrations, other languages |
| WASM modules | wazero, in-process | grammars, pure transforms |
| Native Go | compiled in | the built-ins, and plugins linked with `oriel build --with` |

An installed plugin always enters through Lua, which starts its processes and wasm modules. Hooks can observe, veto,
rewrite or decide an event. Plugins are tested through the real app with `app/plugintest`. The Lua API is in
[api.md](api.md).

## Sessions and kinds

A session is a workspace (plan docs plus a git worktree per repo, on the session's branch), a kind, an agent and
what accrues: threads, tasks, the review ledger, test runs, the transcript. Your own checkouts are never edited,
except by a `direct` session, which opens a directory in place.

A kind (work, plan, review, job, new repo, direct, or one a plugin adds) is a `registry.SessionKind` registered by the
plugin that owns it. Code asks the kind (`registry.Can`, `Label`, `Chips`, kind-scoped contributions) rather than
comparing `core.Kind*` values or reading another kind's data. A kind's own files say so with a `//oriel:kind <kind>`
line, and only they may name it.

Claude runs as its interactive CLI in a PTY, with Oriel's mod for hooks, never `claude -p` (that path is billed per
token). Codex runs as `codex app-server` over stdio.

## Rendering and the toolkit

- `render` takes display lists built each frame; `render/gio` draws them in the window and `render/raster` on the CPU,
  blending in sRGB, for tests.
- `ui` is immediate-mode: widgets are functions over a `*ui.Ctx`, their state keyed by id. Views are composed from
  `ui/widget`; a view never builds its own clickable. If no widget fits, add a general one with its theme tokens.
- Behaviour lives in the toolkit, so every screen gets it:
  - Floats sit on named layers (`ui.LayerPage`, `ui.LayerDialog`…), never numeric `Z`. One under `ui.LayerPane` is
    inlaid in its view and clipped as the view is, so it scrolls out with it.
  - A full-window page is `widget.Page`; a dialog is `widget.Scrim`. Every closable surface has Escape and a visible
    close.
  - Focus has one owner, the toolkit. A view marks its default target and moves focus on purpose with
    `Ctx.RequestFocus`.
  - Text never runs out of its box: a row too narrow cuts its text with "…". Strips of tabs scroll sideways
    (`widget.Strip`); tables scroll (`ScrollX`); a chip that must stay whole is `Keep`.
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
