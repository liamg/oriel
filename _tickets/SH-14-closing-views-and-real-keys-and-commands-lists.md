---
id: SH-14
title: "Closing views, and :Keys/:Commands as real lists"
phase: P3
status: done
area: Shell and navigation
module: app
depends_on: [FW-9, SH-5]
---

# SH-14 · Closing views, and :Keys/:Commands as real lists

**Phase** 3 · shell · **After** [FW-9](FW-9-layout-tree-and-tab-groups-drag-any-pane-to-any-.md), [SH-5](SH-5-pickers-files-grep-k-keymaps-with-preview-and-ac.md) · **Unblocks** [SH-15](SH-15-start-view-what-the-window-shows-with-nothing-open.md)

## Design

- `_design/ui.md` (01d · Layout)
- `_design/plugins.md` (views, keymaps, picker sources)
- `_design/features.md` (this row)

## What

- **Every view can be closed from the keyboard.** A view in the layout that has focus but no key mode of its own
  resolves keys in the `view` mode (`app.ViewKeyMode`) before `n` and `global`. There, `q`, `<C-w>c` and `<C-w>q`
  run `view.close`. The editor has its own modes, so vim's `CTRL-W c`, `:q` and `:close` keep their meaning.
- **Any view opts in the same way**: a plugin's view gets these keys for free, and can map more in `view`.
- **Nothing can trap you.** With a view filling the main area, `⌘K`, `<C-w>` keys and the tab's close button all work.
- **`:Keys` and `:Commands` open the pickers** (`keymaps`, and the omnibox's `commands` source as a picker): filterable,
  scrollable, `<CR>` runs. The plain-text `keys` and `commands` views go.

## Done when

a snapshot test opens each built-in view and closes it with `<C-w>c` and, where read-only, `q`; `:Commands` filters
and runs a command on `<CR>`; no registered view leaves the layout without a way out

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
