---
id: terminal
covers: [terminal]
related: [layout/panes, editor/pane]
---

# Terminal

`:term` opens a shell docked at the bottom; `:term!` opens one in the main area, `:vterm` and `:sterm` in a split.
A terminal is a pane like any other: `⌃W M` moves it, `⌃W Z` zooms it, `` ⌃` `` drops it down from the top.

Keys go to the program, every ⌃ key included (`⌃W` deletes a word in the shell); ⌘ shortcuts still work. `⌃Esc`
(or vim's `⌃\ ⌃N`) switches to **terminal-normal** mode, where vim's motions, search, Visual mode and yank work over
the screen and its scrollback; `i` or `a` goes back. The status line says which mode you're in. To move to another
pane from a terminal, `⌃Esc` then `⌃W h`/`j`/`k`/`l`.

## Shell integration {#shell}

With a shell that marks its prompts (OSC 133) each command gets a dot by its prompt: green if it exited 0, red if not.
In terminal-normal mode `[[` and `]]` jump between prompts. `term.copy_last_output` copies the last command's output,
`term.send_last_output` sends it to a request, and `term.rerun_last` runs it again in a new split. When the shell
reports its directory (OSC 7), the tab shows it.

When Oriel quits, each terminal's screen and scrollback are kept, and come back when it opens the directory again.

{{keys "term*"}}
