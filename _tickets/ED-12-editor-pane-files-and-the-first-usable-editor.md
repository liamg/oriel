---
id: ED-12
title: "Editor pane and files: open, edit, save — the first usable editor"
phase: P3
status: todo
area: Editor
module: editor
depends_on: [ED-4, ED-6, FW-6, FW-9, SH-13]
---

# ED-12 · Editor pane and files: open, edit, save — the first usable editor

**Phase** 3 · **After** [ED-4](ED-4-macros-folds-splits-buffers-quickfix.md), [ED-6](ED-6-tree-sitter-highlighting-folds-outline-code-unit.md), [FW-6](FW-6-widgets-buttons-chips-inputs-virtual-lists-trees.md), [FW-9](FW-9-layout-tree-and-tab-groups-drag-any-pane-to-any-.md), [SH-13](SH-13-errors-notifications-and-the-log-panel.md) · **Unblocks** everything that shows a buffer (ED-7, SH-5, FG-1, LS-1…)

## Design

- `_design/editor.md`
- `_design/ui.md`, `_design/theming.md`
- `_design/plugins.md` (the pane is a registry `View`; keys go through the keymap registry)
- `_design/testing.md` (display-list snapshots, golden PNGs)
- `_design/features.md` (this row)

## Mockups

![01-ide](../_design/mockups/01-ide.png)

The editor column only: tab strip, breadcrumb, gutter, text, cursor, status line mode and position.
Threads, the minimap, the agent dock and the side panels are other tickets.

## What

- **Editor pane**, a registry `View` any layout slot can hold:
  - the buffer's text through the `render` seam, with ED-6's highlights and theme tokens
  - line numbers, the cursor with a shape per mode, Visual selections (char, line, block)
  - closed folds as one line, scrolling that keeps the cursor in view
  - the `NORMAL`/`INSERT`/`VISUAL` chip and the position in the status line
  - virtual rendering (only visible lines) so large files stay fast
- **Keys** from the window go through the keymap registry first, then to the vim engine.
- **The engine's windows are FW-9's panes.** `:split`, `CTRL-W` and friends drive the layout tree, and dragging panes
  in the UI is seen by the engine. One model, not two. `zt`/`zz`/`zb`, `CTRL-E`/`CTRL-Y`, `CTRL-D`/`CTRL-U`/`CTRL-F`/`CTRL-B`
  and `H`/`M`/`L` use the pane's real height.
- **Part of the IDE, not a program inside it** (`editor.md`):
  - the engine's notices go through the IDE's notification handling (SH-13, `errors.md`), with Oriel's
    wording and no vim error numbers; `:messages` opens the history, and `Question` notices are the IDE's confirm
  - the `:` line is an IDE input with completion of ex commands, buffers and files, and it also runs palette commands
  - buffers are the IDE's open files and tabs: `:e`, `:b`, `:bd` and `CTRL-^` open, switch and close tabs, and `:ls`
    opens the switcher
  - quickfix and location lists show in the IDE's results panel, with `:cn`, `:copen` and the rest driving it
  - `y`, `d`, `c`, `x` and `p` use the system clipboard through the IDE's clipboard service, as nvim with
    `clipboard=unnamedplus`; `"*` is the primary selection on Linux and the clipboard on macOS. Named registers stay
    the engine's. ED-13 makes this a setting.
  - `:map`, `:noremap` and `:abbreviate` write to the registry keymap, so they show in the keybinding UI
- **Files** in `edit`:
  - load and save, UTF-8 with or without a BOM, LF and CRLF kept as found; a file that isn't valid UTF-8 or looks
    binary opens read-only with a message
  - atomic writes (temp file and rename) that keep the file's mode
  - the modified flag tied to the undo state, so undoing back to the saved text clears it
  - detecting changes on disk: reload silently when the buffer is unchanged, ask when it isn't
- **Ex commands**: `:w`, `:w {file}`, `:sav`, `:wa`, `:q`, `:q!`, `:wq`, `:x`, `ZZ`, `ZQ`, `:e {file}`, `:e!`, and
  `:qa`, giving notices like the rest (`vim.unsaved`…).
- **`oriel {file…}`** opens the app on those files.

## Done when

- Display-list snapshots of the editor pane on a fixture (normal, insert, Visual block, a closed fold, a split)
  match their goldens, and a golden PNG of the fixture holds against the mockup's editor column.
- A headless test opens a file, edits it through keys, `:w`s, and the bytes on disk are exactly what vim writes for
  the same keys (checked against nvim with `:w`), for LF, CRLF and BOM files.
- Undoing back to the saved text clears modified; `:q` on a modified buffer gives the `vim.unsaved` error notice,
  shown as an IDE notification, where nvim gives E37.
- `yy` in the editor puts the line on the system clipboard (checked through a fake clipboard service), and `p` pastes
  what another app copied.
- `:ls`, `:copen` and `:messages` open the IDE's switcher, results panel and notification history, not text.
- A change on disk reloads an unchanged buffer and prompts for a changed one.
- `:split` and `CTRL-W` commands and a drag in FW-9's layout change the same tree.
- Keypress to frame stays under 8 ms (`PLAN.md`) for typing in a 10,000-line Go file, measured in a benchmark.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
