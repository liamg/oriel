---
id: SH-1
title: "Window, GPU rendering, HiDPI and 1× text"
phase: P0
status: todo
area: Shell and navigation
module: render, typeset
depends_on: [P0-4, P0-6]
---

# SH-1 · Window, GPU rendering, HiDPI and 1× text

**Phase** 0 · spike and repo · **After** [P0-4](P0-4-gio-spike-measure-the-budgets-re-create-01-ide-n.md), [P0-6](P0-6-glyph-atlas-prototype-in-the-spike-re-measure-on.md) · **Unblocks** [FW-4](FW-4-ui-toolkit-core-ctx-ids-flex-layout-anchors-scro.md)

## Design

- `_design/ui.md`
- `_design/shell.md`
- `_design/rendering.md`
- `_design/help.md`
- `_design/theming.md`
- `_design/features.md` (this row)

## Mockups

![01-ide](../_design/mockups/01-ide.png)

## Done when

spike budgets met on macOS and Linux (`shell.md`); `01-ide` re-created natively and side by side with the PNG

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
