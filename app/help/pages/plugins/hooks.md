---
id: plugins/hooks
covers: [hooks]
related: [plugins/lua]
---

# Hooks

`:Hooks` lists every hook: the event, what it does, whether it observes, vetoes, rewrites or decides, who
registered it, and its p99 time. A hook past the 2 ms hot-path budget is marked. A hook on an event nothing fires
yet says what it's waiting for; one that crashed is turned off, and says why.

`L.on(event, opts, fn)` adds one: `fn(ev)` returns nothing to observe, `false, reason` to veto, the changed `ev` to
rewrite, or a value to decide. `opts.pattern` is a glob over the event's file.
