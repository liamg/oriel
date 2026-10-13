---
id: ED-13
title: "Editor options in the registry: :set, the settings page, editor.clipboard"
phase: P3
status: todo
area: Editor
module: vim, registry
depends_on: [ED-12, SH-12]
---

# ED-13 · Editor options in the registry: `:set`, the settings page, `editor.clipboard`

**Phase** 3 · shell · **After** [ED-12](ED-12-editor-pane-files-and-the-first-usable-editor.md), [SH-12](SH-12-settings-page-settings-sections-search-typed-co.md) · **Unblocks** nothing yet

## Design

- `_design/editor.md` (options and defaults; part of the IDE, not a program inside it)
- `_design/settings.md` (`OptionSpec`, precedence, the option list)
- `_design/plugins.md` (options are a registry contribution)
- `_design/testing.md`
- `_design/features.md` (this row)

## What

- **The engine's options become registry `OptionSpec`s**, at the input layer, with vim's names as aliases:
  `shiftwidth`, `expandtab`, `ignorecase`, `smartcase`, `wrapscan`, `magic`, `report`, `nrformats`, `hidden`, and
  whatever the engine has grown by then. `vim.Options` is read from the registry at the buffer's `At{…}`, not kept
  as the engine's own copy.
- **`:set`** reads and writes the session, buffer or window layer, as `settings.md` says. `:set ic` and the
  settings page change the same value, and the page shows `:set` as the value's source.
- **`editor.clipboard`**: what plain `y`, `d`, `c`, `x` and `p` use.
  - `system` (default): the system clipboard, as nvim with `clipboard=unnamedplus` (ED-12's behaviour).
  - `registers`: vim's own unnamed register; the clipboard only through `"+` and `"*`.
- **The option list** in `settings.md` is checked against the registry: a test fails when an option is registered
  without a row, or a row names an option that isn't registered.

## Done when

- Every engine option is registered with its alias, and the `.vt` suite passes with the options read from the registry.
- `:set sw=2` in one buffer changes only that buffer, shows on the settings page with `:set` as its source, and a
  settings-page change is seen by the next `>>`.
- With `editor.clipboard = "registers"`, `yy` leaves the system clipboard alone and `"+yy` sets it; with `system`,
  `yy` sets it.
- The option-list test passes.

## From ED-12

- The clipboard is on for the editor today (unnamedplus, through `edit.Clipboard`); `editor.clipboard` makes it a
  setting. `"*` is the clipboard on Linux until there's a primary selection.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
