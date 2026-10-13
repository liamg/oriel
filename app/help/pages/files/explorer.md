---
id: files/explorer
covers: [explorer]
related: [shell/rail, layout/panes]
---

# Explorer

The work tree, in three scopes. `⌥1` `⌥2` `⌥3` switch between them, and each session remembers its own.

## Scopes {#scopes}

- **Changed**: every file the branch changes against its base (the merge-base with origin's default branch, or
  main), uncommitted work included. `⏎` opens a file's diff.
- **Uncommitted**: the work tree against HEAD, in Staged, Unstaged and Untracked.
- **All**: every file, marked as Changed marks it. Ignored directories are dimmed and not opened.

## Changed {#changed}

Each file has a dot: `●` uncommitted only, `◉` committed with more on top, `○` committed only. **tree** nests
files by directory; **flat** lists them by path.

## Columns {#columns}

In All, the chips turn columns on and off: **status** (the letter, and the name tinted to match), **± lines**
(with a bar for the size of the change) and **diag** (error and warning counts). Folders show a dot and how many
changed files are under them. **changed-only** (`⌥C`) narrows All to the changed files.

## Keys {#keys}

In Uncommitted, fugitive's keys: `s` stages the file (or unstages a staged one), `u` unstages, `x` discards its
unstaged changes after asking (an untracked file is deleted), `=` opens its diff.

{{keys "explorer.*"}}
