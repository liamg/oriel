---
id: plugins/lua
covers: [lua]
related: [plugins/api, plugins/hooks, plugins/inspect, shell/settings]
---

# Lua

Your config is `~/.config/oriel/init.lua`, with modules under `lua/` for `require`. It runs in one Lua VM with the
`oriel` API (write `local L = oriel` for a short name): options, keymaps, commands, hooks, views, decorations, and registrations for agent
tools, prompt packs, session kinds, HUD indicators and status rules. Saving it reloads it, with a toast; an error says
where.

## When it fails {#errors}

An error says where, as `init.lua:14` or `lua/my/tickets.lua:8`, with the Lua traceback: frames in the `oriel` API
show as the call (`oriel.agent.tool`), and the Go frames under it are behind **show**. It's a notice with a link to
the line, a mark on the line, and a row in `:Problems`.

- A config that fails to reload leaves the last good one running. At startup, what ran before the error is
  running, and the toast's **Edit init.lua** opens the line. Saving a fix reloads it and clears the toast.
- A hook, view or decoration that errors 3 times in a row is turned off, with **Turn back on**.

## The REPL {#repl}

`:lua` opens it; `:lua <code>` runs one line. A line is tried as an expression first, so `oriel.api.version` shows its
value. `⇥` completes names under `oriel`, `↑` brings back earlier lines. An error shows with its trace
under the line.

## UI {#ui}

`L.view.override(id, fn)` registers a view: a new one (a panel, opened with `view.open` in a dock, a split or a
tab), a segment of the title bar or status line (`titlebar.right.*`, `statusline.left.*`, `statusline.right.*`),
or a built-in it replaces. `fn` returns `L.ui` nodes: `row` and `column` (with `gap` and `pad`), `text`, `spacer`,
`indicator`, `ring` (a dial with a short label in it), `list`, `button` (`on_click` runs Lua), `input`, `toggle` and `select` (their `on_change` gets the new value),
and `mode`. `oriel.ui.state(key, initial)` is a table kept across redraws, for what a view shows. Tab moves between
controls. Any node can be dragged (`drag = { kind, data, label }`) or take drops (`accepts`, `on_drop`), by pointer
or keyboard (Space picks up and drops). `oriel.decor.provider` marks lines in the editor, and `oriel.menu.add` adds a
right-click entry.

## Options {#options}

`oriel.option.name = value` sets your settings (by name or vim alias, at the Lua layer, which beats settings.toml);
`oriel.buffer_option` the current buffer's; `oriel.session_option` the session's. A value for an option nothing has registered yet waits for it.
`oriel.option{ name, type, default, help, values }` declares one, for the settings page and settings.toml; a
plugin's are named `plugin.<name>.…` and saved under `[plugin.<name>]`.

## Time and programs {#time}

`L.now()` is Oriel's clock in seconds (use it, not `os.time`, for anything you show: tests can fix it), and
`L.platform` the OS (`darwin`, `linux`). `L.exec(argv, { on_done = fn(output, err) })` runs a program without holding
up the window.

## Later features {#later}

Namespaces for features that aren't here yet (`L.linear`, `L.thread`, `L.lsp`, `L.coverage`,
`L.http`) load, and calling into one says which ticket brings it.

## Types {#types}

`script/types/oriel.d.lua` has LuaLS annotations for the whole API, generated from the same Go as the bindings.
