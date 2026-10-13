---
id: shell/start
covers: [app.start]
related: [shell/rail, shell/settings, shell/omnibox]
---

# The start view

What the window shows with nothing open. On the left, what you can start; on the right, the directories you've
opened lately.

- **New Session** and **Resume a Session** are dimmed until sessions arrive.
- **Open Directory** opens a directory as it is: no worktree, no new branch. Type a path, pick a recent or a repo
  under your home directory, or choose **Browse…** for the system's folder chooser. `oriel <dir>` does the same.
- `j` and `k` move, `↵` runs, `⌫` forgets a recent directory.
- Plugins add their own actions with `oriel.start.add`.

{{keys "directory.open"}}
