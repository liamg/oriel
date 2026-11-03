---
id: terminal
covers: [terminal]
related: [layout/panes, editor/pane]
---

# Terminal

`:term` opens a shell docked at the bottom; `:term!` opens one in the main area, `:vterm` and `:sterm` in a split.
A terminal is a pane like any other: `⌃W M` moves it, `⌃W Z` zooms it, `` ⌃` `` drops it down from the top.
Right-click its tab for **Rename…** (`term.rename`): a name of your own in place of the program's; an empty one puts
the program's back.

Keys go to the program, every ⌃ key included (`⌃W` deletes a word in the shell); ⌘ shortcuts still work. `⌃Esc`
(or vim's `⌃\ ⌃N`) switches to **terminal-normal** mode, where vim's motions, search, Visual mode and yank work over
the screen and its scrollback; `i` or `a` goes back. The status line says which mode you're in. To move to another
pane from a terminal, `⌃Esc` then `⌃W h`/`j`/`k`/`l`.

Drag to select text on the screen or in the scrollback (a double click selects a word, a triple the row), then ⌘C, or
⌃⇧C on Linux, copies it. ⌘A selects the whole screen and scrollback. With nothing selected, ⌘C goes to the
program. ⌘V (⌃⇧V) pastes.

The wheel scrolls the scrollback, unless the program asked for the mouse (a TUI such as Claude Code or vim with the
mouse on): then the wheel goes to it, as a real terminal sends it. A full-screen program that turned on alternate
scroll gets ↑ and ↓ instead. Hold Shift to scroll the scrollback anyway.

## Shell integration {#shell}

With a shell that marks its prompts (OSC 133) each command gets a dot by its prompt: green if it exited 0, red if not.
In terminal-normal mode `[[` and `]]` jump between prompts. `term.copy_last_output` copies the last command's output,
`term.send_last_output` sends it to a request, and `term.rerun_last` runs it again in a new split. When the shell
reports its directory (OSC 7), the tab shows it. If the last command failed, right-click the terminal for **Ask
the agent to fix the failed command** (a request with the command, its output and its exit status) and **Ask the
agent to explain** (an answer in a notice; nothing is changed).

When Oriel quits, each terminal's screen and scrollback are kept, and come back when it opens the directory again.

{{keys "term*"}}
