---
id: editor/pane
covers: [editor]
related: [layout/panes, shell/pickers]
---

# The editor

A pane shows one window on a buffer. Keys go to the input engine (vim by default) after the keymaps you've made.

- The tab is the buffer's file, with a dot while it has changes not written.
- `:w` writes, `:e file` opens, `:split` and `⌃W` move between panes, which are the layout's.
- `y`, `d` and `p` use the system clipboard, and so do ⌘C, ⌘X and ⌘V (⌃⇧C, ⌃⇧X, ⌃⇧V on Linux) in any mode: copy
  and cut the selection, paste at the cursor or over the selection.
- The mouse selects as in gvim: drag to select, double-click for a word, triple-click for the line, ⇧-click to
  extend, ⌥-drag for a block. Dragging past the top or bottom scrolls. ⌘A selects the whole buffer.
- Long lines run past the edge: the window scrolls sideways to follow the cursor (`editor.side_scroll`,
  `editor.side_scroll_off`), `zl`/`zh`/`zL`/`zH`/`zs`/`ze` scroll it, and so does a sideways wheel or trackpad.
- A file that changes on disk reloads by itself, unless you've changed it too: then Oriel asks.

## Wrapping {#wrap}

`editor.wrap` is off by default. Turn it on with `:set wrap` (`:set wrap!` switches it), the
*Wrap long lines* command, or **wrap** on the status line, which is bright while lines wrap. Wrapped, a long
line continues on the rows below; `gj`, `gk`, `g0`, `g^`, `gm` and `g$` move by those rows, and `j`/`k` by lines.

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
On a line with diagnostics the **Agent** group has **Ask the agent to fix in a thread** (a request with the
diagnostics and the code around them attached) and, beside it, **Ask the agent to explain** (a card under the line;
nothing is changed). Without a language server, a line with diagnostics still shows the Agent group.

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
cargo, or a plugin's); click it to run that one, `⇧`-click to debug it, or right-click it for **Run test**, **Debug
test**, **Run with coverage** and **Run file**. After a run each shows `✓`, `✕` or `◌` (flaky) and how long it took.
The keys are LazyVim's: `␣tt` runs the file's tests, `␣tr` the one at the cursor, `␣tT` everything, `␣tl` the last
run again, `␣tS` stops a run and `␣tw` turns watch on; `␣tp` runs the package's, `␣tF` what failed last time, and
`]t` goes to the next failure. The buttons at the top right run the file, re-run
failures, turn **watch** on (a save re-runs the tests it affects) and run with **coverage** (covered lines are marked
green in the gutter, the rest red).

A failure shows under its assertion: want and got side by side with what differs marked, the output, and **jump to
assertion**, **debug this case**, **ask agent to explain** (an answer under the failure; nothing is changed) and **ask
agent to fix**. The last three need the debugger and the agent, and are dimmed until they're there.

The Tests panel (`␣ts`) has every test by package, with its recent runs, the failed-only and slowest filters, flaky
tests marked, and the output of the one picked, of the whole run, or the raw JSON. `␣to` shows the output of the test
at the cursor, `␣tO` the whole run's, and `␣ta` follows the running test's as it comes.

{{keys "test.*"}}

## Explain by code unit {#explain}

A `?` in the gutter marks each code unit the branch changed: the smallest function, method, type, `case`, `if`, loop
or closure that owns a change against the branch's base (tree-sitter finds them, in every language with a units
query). Click it, or put the cursor in a unit and press `␣e`, to explain it in a card under the unit. The card's
breadcrumb (`func roundHalfEven › switch diff › case 0`) shows where you are; click a crumb, or press `[` and `]`
while the cursor is in the unit, to widen and narrow along the syntax tree, and `␣e` again widens. In Visual mode
`␣e` explains the selection. **Brief**, **detailed** and **why · history** (which adds git blame, the commits, the
session's PR and the threads on those lines) ask again at another depth.

Answers come from the first explainer: the session's agent, forked without tools, so nothing changes. They're
cached by the unit's text: an edit inside the unit marks the card **changed since it was explained**, with
**refresh**; edits elsewhere keep it. **Ask a follow-up** writes a request with the explanation attached, and **Pin
as note** keeps it as a resolved thread on those lines. The right-click menu has **Explain this**, and **Review
this**, **Write tests for this** and **Refactor this…**, which start a request about the selection or the unit.

{{keys "explain.*"}}

## Changed units {#changed-units}

The Changed units panel (`explain.units`, or **explain by unit** above the editor) lists every unit the branch
touched, by file and nested as the code is, with whether it's been explained, is open, has changed since, or not
(`–`). Opening one goes to it and explains it, so explaining by unit doubles as a review checklist.

## Explain this change {#explain-change}

**Explain this change** (`explain.change`, `␣E`, or the omnibox) asks the session's agent, forked without tools, for
a walkthrough of the branch's whole change: every changed file in every project, against where the branch left its
base. It answers in steps, each about some lines of a file, with notes pinned to parts of them, and ends with a
summary. The step's file is shown with the lines its notes are on lit in their colours and the rest dimmed, each note
beside its lines with a leader line. The dock along the bottom has a chip for every step across files and repos;
click one, or press `←` and `→`, to move between them. `j` and `k` pick the line **Comment on** opens a thread on
(`c`), and **Ask about this step** (`a`) writes a request with the step attached. `q` or Escape closes the
walkthrough; Explain this change brings it back as it was.

A walkthrough is throwaway: once the lines a step is about change (in the editor or on disk), it's discarded, and
Explain this change asks for a new one. **Discard** throws it away now.

{{keys "explain.change*"}}

## Presentations {#present}

A file can be shown as something other than its text: a markdown file as a document (its **Preview** button,
`:MarkdownPreview`, or `␣cp`), or as a plugin's presenter draws it (`editor.present`; `␣cp` takes the first that
takes the file). The file stays open and editable, and
the presentation follows its text, so two windows on one file can show it both ways at once. The file's threads sit
in the margin beside the blocks they're about. `j`/`k` move between blocks, `c` or `⏎` (or a click) opens a thread on a
block's lines, and `o` goes to its source. A table's rows are blocks of their own here: a thread on a row's line
sits beside that row. A `mermaid` fence (a flowchart or a sequence diagram) is drawn as its diagram, scaled
to the column; click it to zoom it, with the document's images. **Edit** (or `␣cp` again) shows the text again, where you were.

## Threads in a file {#threads}

A file's threads open between its lines. `␣R` (`threads.toggle`), or the 💬 button with their count, hides them
behind speech bubbles in the gutter, coloured by the thread's state; `editor.threads = "hidden"` opens files that way.
Hovering a bubble shows its threads without taking the keys; a click, or `⏎` on its line, shows them again at that
thread with its reply field ready.
