---
id: plugins/lua
covers: [lua]
related: [plugins/hooks, plugins/inspect, shell/settings]
---

# Lua

Your config is `~/.config/oriel/init.lua`, with modules under `lua/` for `require`. It runs in one Lua VM with the
`oriel` API (also called `L`): options, keymaps, commands, hooks, views, decorations, and registrations for agent
tools, prompt packs, session kinds, HUD tats and status rules. Saving it reloads it, with a toast; an error says
where.

## When it fails {#errors}

An error says where, as `init.lua:14` or `lua/my/tickets.lua:8`, with the Lua traceback: frames in the `oriel` API
show as the call (`oriel.agent.tool`), and the Go frames under it are behind **show**. It's a notice with a link to
the line, a mark on the line, and a row in `:Problems`.

- A config that fails to reload leaves the last good one running. At startup, what ran before the error is
  running, and the toast's **Edit init.lua** opens the line. Saving a fix reloads it and clears the toast.
- A hook, view or decoration that errors 3 times in a row is turned off, with **Turn back on**.

## The REPL {#repl}

`:lua` opens it; `:lua <code>` runs one line. A line is tried as an expression first, so `L.api.version` shows its
value. `⇥` completes names under `oriel` and `L`, `↑` brings back earlier lines. An error shows with its trace
under the line.

## UI {#ui}

`L.view.override(id, fn)` registers a view: a new one (a panel, opened with `view.open` in a dock, a split or a
tab), a segment of the title bar or status line (`titlebar.right.*`, `statusline.left.*`, `statusline.right.*`),
or a built-in it replaces. `fn` returns `L.ui` nodes: `row` and `column` (with `gap` and `pad`), `text`, `spacer`,
`tat`, `list`, `button` (`on_click` runs Lua), and `mode`. `L.decor.provider` marks lines in the editor.

## Options {#options}

`L.o.name = value` sets your settings (by name or vim alias, at the Lua layer, which beats settings.toml);
`L.bo` the current buffer's; `L.so` the session's. A value for an option nothing has registered yet waits for it.

## Later features {#later}

Namespaces for features that aren't here yet (`L.ledger`, `L.linear`, `L.thread`, `L.lsp`, `L.coverage`,
`L.http`) load, and calling into one says which ticket brings it.

## Types {#types}

`script/types/oriel.d.lua` has LuaLS annotations for the whole API, generated from the same Go as the bindings.
