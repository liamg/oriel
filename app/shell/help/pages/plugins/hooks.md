---
id: plugins/hooks
covers: [hooks]
related: [plugins/lua]
---

# Hooks

`:Hooks` lists every hook: the event, what it does, whether it observes, vetoes, rewrites or decides, who
registered it, and its p99 time. A hook past the 2 ms hot-path budget is marked, and one over it on two calls running
is a warning in the log: one call alone may have been held up by a busy machine. A hook on an event nothing fires yet says what it's waiting for. One that panics is turned off at once,
and one that errors 3 times in a row is too; `:Hooks` shows it off with its last error, and its notice has
**Turn back on**.

`L.on(event, opts, fn)` adds one: `fn(ev)` returns nothing to observe, `false, reason` to veto, the changed `ev` to
rewrite, or a value to decide. `opts.pattern` is a glob over the event's file.
