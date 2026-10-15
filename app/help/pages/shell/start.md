---
id: shell/start
covers: [app.start]
related: [shell/rail, shell/settings, shell/omnibox]
---

# The start view

What the window shows with nothing open. On the left, what you can start; on the right, your sessions and the
directories you've opened lately.

- **New Session** opens the launcher, and **Resume a Session** picks a session; both are dimmed while no worker is
  connected.
- **Open Directory** opens a directory as it is: no worktree, no new branch. Type a path, pick a recent or a repo
  under your home directory, or choose **Browse…** for the system's folder chooser. `oriel <dir>` does the same.
- `j` and `k` move, `↵` runs, `⌫` forgets a recent directory; so does the `✕` that shows on one under the pointer.
  A forgotten directory stays gone until you open it again.
- Plugins add their own actions with `oriel.start.add`.
- Only the start view's own keys work here: its rows', and its actions' and links' (⌘N, ⌘O, ⇧⌘O, ⌘, and the pages').
  Search (⌘K), commands (`:`) and the editor's keys wait until a directory or session is open.

## Sessions

Every session that isn't archived, newest first, with its kind, projects and branch; the diamond is its status
colour. Opening one brings it to the front, resuming its agent if it was stopped.

## Links

Under the actions, small links to the pages every session shares, each with its headline figure: **Stats** (this
week's cost), **Usage** (the weekly window), **Jobs** and **Workers**. A plugin adds one with
`oriel.start.add{ link = true, note = fn }`.

{{keys "directory.open"}}
