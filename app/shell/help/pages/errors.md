---
id: errors
covers: [app.notifications, log, problems, statusline.left.50-counts]
related: [shell/help, editor/pane]
---

# Errors and notifications

Everything Oriel tells you goes through one place: toasts while it's fresh, the ✕ and ⚠ counts in the status line,
and the log, where all of it stays.

## Toasts {#toasts}

A toast is a notice while it's fresh. Info goes after 3 seconds, a warning after 8 (or stays while the pointer is on
it), and an error stays until you dismiss it or open its details. An error from a session that isn't in front
doesn't toast: it marks that session's tab instead. A notice that asks you something (a question) is an [inbox](shell/inbox#notices) card instead.

- Click an action to do it, or **Details** (**Show trace** for an error with a trace) to open the log at that
  entry.
- The ✕ dismisses it; it stays in the log.

## The log {#log}

`:Log` (or `:messages`) opens the log at the bottom. It lists every notice and Oriel's own log records, newest
last, in three scopes: Oriel's, the session in front, and all of them together.

- The chips filter by severity; the box filters by text, or `source:init.lua`.
- Click an entry to open its stack trace and the source around the failing line.
- **Copy** copies the open entry, or everything shown; **Copy report** copies a report for a bug.
- `:Log level debug` shows debug records from now on.

{{keys "Log"}}
{{keys "log.*"}}

## Problems {#problems}

`:Problems` lists every diagnostic, by file and line: errors in your Lua config, and later language servers'. The
editor marks each on its line: a sign, the span underlined, the message after it, and an error's line tinted. Click
a problem to open it.

## Crash reports {#crash}

When Oriel itself fails, it writes a crash report and offers it the next time it starts. The report stays on your
machine: nothing is sent. Copy it into a new issue if you'd like it fixed, or delete it.
