---
id: editor/pane
covers: [editor]
related: [layout/panes, shell/pickers]
---

# The editor

A pane shows one window on a buffer. Keys go to the input engine (vim by default) after the keymaps you've made.

- The tab is the buffer's file, with a dot while it has changes not written.
- `:w` writes, `:e file` opens, `:split` and `⌃W` move between panes, which are the layout's.
- `y`, `d` and `p` use the system clipboard.
- A file that changes on disk reloads by itself, unless you've changed it too: then Oriel asks.

{{options "editor"}}
{{keys "editor.*"}}

## Git hunks {#hunks}

The gutter marks lines that differ from the index: `▎` added or changed, `▁` where lines were deleted. Unsaved
edits count. On a hunk, `␣hs` stages it, `␣hu` unstages the staged hunk there, `␣hr` puts it back as the index has
it (one undo step), and `␣hp` shows the lines it replaced. `]c` and `[c` move between hunks. After the cursor's line,
who last changed it and when: you, an agent (by its name, or a Co-Authored-By trailer), someone else, or "Not
committed yet". `␣hb` (or `git.blame.inline`) turns that off.

{{keys "git.hunk.*"}}

## Language servers {#lsp}

A file whose language has a server installed (gopls, typescript-language-server, pyright, rust-analyzer, clangd and
others) gets one, started for its project root. Its state is in the status line, its errors and warnings are marked
in the file and listed in Problems, and its stderr goes to the log.

## Completion {#completion}

As you type a word (or after `.`), the server's completions show under the cursor, the letters you typed lit, with the
highlighted one's docs beside them. `⌃n`/`⌃p` or the arrows move, `⏎`, `⇥` or `⌃y` take it, `⌃e` closes the menu.
`⌃␣` asks for completions. A completion's snippet puts the cursor at its first stop.

## Signature help {#signature}

Typing a call's `(` or `,` shows its signature above the cursor, the parameter you're on lit.

## Hover {#hover}

`K` shows what the server knows about the symbol under the cursor; it closes when the cursor moves, or with `esc`.
Without a server, `K` is vim's.

## Code actions {#code-actions}

`␣ca` (or `gra`) lists the server's fixes and refactors for the cursor's line, by kind. Taking one makes its edit.

## References {#references}

`grr` peeks at every reference to the symbol under the cursor, under its line: the list by file, and the code of the
highlighted one. `j`/`k` move, `⏎` goes there, `esc` closes it. `gd` goes to the definition (`gD` the declaration, `gy`
the type, `gri` the implementations); several go to the quickfix list. `gO` lists this file's symbols, `␣S` searches
the workspace's, and `␣ci`/`␣co` list a function's callers and callees.

## Rename {#rename}

`grn` (or `␣rn`) asks for a new name, then shows the rename as a diff of every file it changes. Apply makes it: open
files change in their buffers, others are written.

{{keys "lsp.*"}}

## Multiple cursors {#multi}

`⌃n` selects the word under the cursor; each `⌃n` after it adds the next whole-word match. `⌃↓` and `⌃↑` add a
cursor on the line below or above. With words selected, `c` (or `s`) changes them all, `d` (or `x`) deletes them, `I`
and `A` type before or after each, `o` swaps their ends, `q` skips the last match for the next, `Q` drops it, and `⇥`
switches to plain cursors. With cursors, any normal-mode command runs at every one, and insert mode types at all of
them. Each command, with the typing after it, undoes as one step. `esc` goes back to one cursor.

A `:s` of a whole word (`:%s/\<Mode\>/RoundingMode/g`) shows how many matches it has and, with a language server,
offers its rename: `⇥` renames the symbol instead, after showing the diff.
