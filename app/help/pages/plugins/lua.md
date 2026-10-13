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

## The REPL {#repl}

`:lua` opens it; `:lua <code>` runs one line. A line is tried as an expression first, so `L.api.version` shows its
value. `⇥` completes names under `oriel` and `L`, `↑` brings back earlier lines.

## Options {#options}

`L.o.name = value` sets your settings (by name or vim alias, at the Lua layer, which beats settings.toml);
`L.bo` the current buffer's; `L.so` the session's. A value for an option nothing has registered yet waits for it.

## Later features {#later}

Namespaces for features that aren't here yet (`L.ledger`, `L.linear`, `L.thread`, `L.lsp`, `L.coverage`,
`L.http`) load, and calling into one says which ticket brings it.

## Types {#types}

`script/types/oriel.d.lua` has LuaLS annotations for the whole API, generated from the same Go as the bindings.
