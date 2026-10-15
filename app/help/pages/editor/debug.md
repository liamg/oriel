---
id: editor/debug
covers: [debug, statusline.left.66-debug]
related: [editor/pane, layout/panes]
---

# Debugging

Oriel debugs through debug adapters (the Debug Adapter Protocol): delve for Go and debugpy for Python are built in,
and a plugin adds more by registering a `dap.adapter`. The adapter runs on this machine; what it prints to stderr is in
the log as `dap:<name>`.

## Breakpoints {#breakpoints}

Click a line number, press `␣db` or `F9` to set or clear a breakpoint on a line. `:Breakpoint v < 0` sets a
conditional one (a ring), `:Logpoint x is {x}` a logpoint (a diamond) that prints rather than stops, with the
expressions in braces filled in, and `debug.breakpoint.set {hit = "== 3"}` one that stops on the third hit. A
breakpoint the adapter couldn't place is grey, with its reason after the line. Breakpoints are kept for the directory
between runs, and changing them while a session runs sends them straight away.

## Running {#session}

`F5` starts debugging: the config named in `.oriel/debug.toml`, the first there, or one the adapter works out for the
current file (a Go package's program or its tests, a Python file). A config is a `[[debug]]` table:

```toml
[[debug]]
name = "server"
type = "go"            # an adapter's type or name
request = "launch"     # or attach, with pid = 1234
program = "./cmd/server"
args = ["--port", "8080"]
env = { LOG = "debug" }
mode = "debug"         # anything else is the adapter's own
```

While paused, the line is marked with `➜`; `F5` continues, `F10` steps over, `F11` into, `⇧F11` out, `F6` pauses,
`⇧F5` stops and `⇧⌘F5` restarts. `]f` and `[f` move up and down the stack, and `:Dc` continues.

## The Debug panel {#panel}

The panel shows the call stack (pick a frame to see its variables and its line), the breakpoints with their hit
counts, the selected frame's variables (open a structured one to fetch its fields), watch expressions evaluated in
the selected frame, and the debug console: the program's output, and a REPL that evaluates in the selected frame.

{{keys "debug.*"}}
