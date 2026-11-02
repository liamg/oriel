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
files by directory; **flat** lists them by path; **by review** lists those with the most agent-written lines still
to read first. A file with agent-written lines has a read bar: read, then skimmed (the review ledger, see the
diff's help).

## Columns {#columns}

In All, the cog at the Explorer's top right turns columns on and off: **status** (the letter, and the name tinted to match), **± lines**
(with a bar for the size of the change), **agent** (`◆` where agent-written lines are still to read, `◇` once
you've read them all) and **diag** (error and warning counts). Folders show a dot and how many
changed files are under them. **Changed only** (`⌥C`) narrows All to the changed files. **Hidden files** shows dot
files (and what's under dot directories) that haven't changed; a changed one always shows.

## Scratchpad {#scratchpad}

In All, a sandboxed session's **scratchpad** sits under the repo: the agent's temp dir, where it keeps screenshots,
notes and other scratch files. Folders read as they open, and what the agent adds shows as it lands; a file opens as
any other.

## Keys {#keys}

In Uncommitted, fugitive's keys: `s` stages the file (or unstages a staged one), `u` unstages, `x` discards its
unstaged changes after asking (an untracked file is deleted), `=` opens its diff. `c` commits what's staged and `C`
everything uncommitted, past the commit gates (**Commit…** does the same).

{{keys "explorer.*"}}
