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

`grn` asks for a new name, then shows the rename as a diff of every file it changes. Apply makes it: open
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

## Tests {#tests}

A `▶` in the gutter marks every test, subtest and table row the file's test runner can run (Go, pytest, vitest, jest,
cargo, or a plugin's); click it to run that one. After a run each shows `✓`, `✕` or `◌` (flaky) and how long it took.
`␣tt` runs the test at the cursor, `␣tf` the file's, `␣tp` the package's, `␣ta` everything, `␣tF` what failed last
time, `␣tl` the last run again; `]t` goes to the next failure. The buttons at the top right run the file, re-run
failures, turn **watch** on (a save re-runs the tests it affects) and run with **coverage** (covered lines are marked
green in the gutter, the rest red).

A failure shows under its assertion: want and got side by side with what differs marked, the output, and **jump to
assertion**, **debug this case**, **ask agent to explain** (an answer under the failure; nothing is changed) and **ask
agent to fix**. The last three need the debugger and the agent, and are dimmed until they're there.

The Tests panel (`␣to`) has every test by package, with its recent runs, the failed-only and slowest filters, flaky
tests marked, and the output of the one picked, of the whole run, or the raw JSON.

{{keys "test.*"}}

## Presentations {#present}

A file can be shown as something other than its text: a markdown file as a document (its **Preview** button, or
`:MarkdownPreview`), or as a plugin's presenter draws it (`editor.present`). The file stays open and editable, and
the presentation follows its text, so two windows on one file can show it both ways at once. The file's threads sit
in the margin beside the blocks they're about. `j`/`k` move between blocks, `c` or `⏎` (or a click) opens a thread on a
block's lines, and `o` goes to its source. **Edit** shows the text again, where you were.

## Threads in a file {#threads}

A file's threads open between its lines. `␣R` (`threads.toggle`), or the 💬 button with their count, hides them
behind speech bubbles in the gutter, coloured by the thread's state; `editor.threads = "hidden"` opens files that way.
Hovering a bubble shows its threads without taking the keys; a click, or `⏎` on its line, shows them again at that
thread with its reply field ready.
