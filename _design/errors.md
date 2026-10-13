# Errors, logs and debugging Oriel

How Oriel tells you something went wrong, where you look later, and how you debug your config, a plugin or Oriel
itself. Built in SH-13 and PL-12. Mockups: `37-errors.png` (toasts, the error in the file, the log panel with a
trace) and `38-crash.png` (the crash report on the next launch).

## One path for every notice

- Everything the user is told goes through one type: an engine's `edit.Notice`, a plugin's `oriel.notify`, a Lua
  error, a failed LSP server, a Go error from a command. Each becomes a core event, `Notified`, so it lands in the
  event log (`architecture.md`) and in `core.State`'s notification history.
- A notice has an ID (`vim.unsaved`, `lua.error`, `lsp.crashed`), a severity (info, warning, error, question), a
  source (`core`, `vim`, `plugin:linear`, `lsp:gopls`, `init.lua`), an optional title ("Config error"), its text, an
  optional detail (a stack trace, the command's stderr), a location (`file:line`), actions ("Open init.lua:14",
  "Disable plugin", "Restart gopls"), and whether it answers a key just typed (which decides how long it toasts).
- The same ID repeating collapses into one entry with a count, so a hook failing on every keystroke is one row.
- Plugins send notices and read the history through the same API (`plugins.md`).

## Two logs: Oriel's and each session's

- **The Oriel log** is the app's: config and plugin errors, rendering, the GUI, update checks, Oriel's own panics.
- **A session log** belongs to one session (`sessions.md`): its agent, worktree, LSP servers, test runs, terminals and
  gateway. It lives with the session on its worker, survives a restart, and is archived with the session.
  Session logs sit behind `logs.SessionLogs`. Until the worker exists (AI-1), the app keeps them itself, one file
  per session under its state dir; AI-1 moves them onto the worker and streams their records to the app.
- A notice goes to exactly one of them, by its source. The log panel switches between **Oriel**, **Session · {name}**
  (the focused one) and **All**, which interleaves them with the session's name on each row.
- Errors from a session you're not looking at don't toast: they put an error mark on that session's tab and wait in
  its log. Its warnings and info still toast, with the session's name on them (mockup 37's gopls restart). The
  status line's ✕/⚠ counts are the focused session's plus Oriel's.

## How they look

Toasts for now, everywhere: it's the one place to look while we find out what's too noisy. Revisit once the editor
is in daily use.

| Notice | Where | Stays |
|---|---|---|
| From a key you just typed (`vim.pattern-not-found`, "3 lines yanked") | a toast; info ones short | info 3 s, errors 8 s |
| Background info ("reloaded `init.lua` · 3 ms", mockup 19) | a small toast at the top right of the focused pane | 3 s |
| Warning | a toast, and the status line's ⚠ count (mockup 12) | 8 s, or until hovered |
| Error not caused by your last key (a plugin, LSP, a hook, a failed save) | a toast with its actions and "Details", and the status line's ✕ count | until dismissed or opened |
| Question from a key (`vim.swap-range`) | a toast with the choices, answered by the next key or a click | until answered |
| Question needing attention while you're elsewhere (approvals, agentbus) | an interrupt card (SH-9, mockup 12) | until answered |
| Oriel can't go on (quit with unsaved buffers, crash recovery on launch) | a modal | until answered |

- No modal for anything that can wait. No sound.
- Clicking the ✕/⚠ counts, or "Details" on a toast, opens the log at that entry.

## The log panel (`:messages`, `:Log`)

- A bottom-panel tab next to Problems and Terminal. It lists the notification history and Oriel's own log records,
  newest last, with filters by severity and source, and search.
- An entry expands to its detail:
  - a **stack trace**: one frame per line, `file:line` and function, each frame jumping to its source
  - the **source around the failing line**, highlighted with tree-sitter (ED-6), the line itself marked
  - for a command that failed, its stderr, highlighted where it has `file:line` references
- "Copy" copies an entry as plain text; "Copy report" copies a bundle for a bug report (below).

## Lua errors (config and plugins)

- A chunk loads with its source name, so every error says `init.lua:14` or `lua/my/tickets.lua:8`, not `[string]`.
- A Lua error carries the Lua traceback. Frames inside the `oriel` API show the API call (`oriel.agent.tool`), not
  Go internals; Go frames are behind "show Go frames".
- Where it shows:
  - an error notice with "Open at line" and "Show trace"
  - a diagnostic on the line in the file when it's open (gutter mark and underline, the same model as LSP
    diagnostics), and an entry in Problems
  - in the `:lua` REPL, under the input that failed
- A config that fails to load keeps the last good config running (as `settings.md` does for TOML), and says so.
- At startup a broken config doesn't block anything: Oriel opens as usual, without the failing part, and shows the
  error toast with an **Edit init.lua** link. The link opens the file in the config session at the failing line;
  saving it reloads the config (it's watched), and the toast clears when the reload succeeds.
- A hook or view that errors 3 times in a row is turned off with a notice ("`tickets.lua:8` turned off after 3
  errors", action "Turn back on"). `:Hooks` shows it as off, with its last error.
- A hook over its time budget is a warning, as mockup 19 shows ("4.1 ms on CommentSend · over the 2 ms budget").

## Go errors and panics

- Every call into a plugin, hook, view, provider or command runs under `recover`. A panic becomes an error notice
  with the Go stack as its detail, the offending contribution is turned off at once (it doesn't wait for 3, as an
  error does), with "Turn back on", and the app keeps running. A command that returns an error is an error notice
  and stays on.
- A panic outside those (Oriel's own bug) writes a crash report (in the state dir's `crashes/`) and exits. The next launch offers it in a modal
  (`38-crash.png`): the report, **Copy report**, a link to open a GitHub issue
  (`github.com/liamg/oriel/issues/new`), and delete. It stays on the machine: nothing is sent anywhere.

## Logs on disk

- Go code logs with `log/slog` (stdlib), structured, with a source attribute.
- Records go to an in-memory ring (the log panel) and a rotating file: `~/Library/Logs/Oriel/oriel.log` on macOS,
  `$XDG_STATE_HOME/oriel/oriel.log` on Linux. Workers log the same way; their records stream to the app.
- LSP servers', DAP adapters' and process plugins' stderr are captured as records with their source.
- Level: `info` by default; `oriel --log-level=debug`, `ORIEL_LOG=debug`, or `:Log level debug` live.
- Secrets never reach a log: values from the secrets store (`security.md`) and tokens in URLs are redacted before
  a record is written.

## Debugging Oriel itself

- `:Log` (above), `:Events` (the live core event stream with filters, from the event log), `:Inspect`, `:Hooks` with
  timings, and the HUD's worker tats.
- `oriel --debug` turns on debug logging and the frame-time overlay.
- "Copy report": version, OS, the settings that differ from defaults, enabled plugins, the last 500 log records and
  the event-log tail, redacted, as one block of text.
