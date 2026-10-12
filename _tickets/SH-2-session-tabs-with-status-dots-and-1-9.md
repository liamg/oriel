---
id: SH-2
title: "Session tabs with status dots and ⌘1–9"
phase: P3
status: todo
area: Shell and navigation
module: app
depends_on: [FW-6, FW-2]
---

# SH-2 · Session tabs with status dots and ⌘1–9

**Phase** 3 · shell · **After** [FW-6](FW-6-widgets-buttons-chips-inputs-virtual-lists-trees.md), [FW-2](FW-2-event-store-typed-events-materialised-core-state.md) · **Unblocks** [SH-7](SH-7-mission-control-k-with-live-terminals-and-inline.md)

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

7 statuses render correctly from a fixture `core.State`; keys switch in under one frame

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
