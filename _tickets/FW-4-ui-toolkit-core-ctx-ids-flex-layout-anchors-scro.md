---
id: FW-4
title: "ui toolkit core: Ctx, ids, flex layout, anchors, scroll, clipping, z-order"
phase: P1
status: todo
area: Framework (first, after the spike)
module: ui
depends_on: [FW-1, SH-1]
---

# FW-4 · ui toolkit core: Ctx, ids, flex layout, anchors, scroll, clipping, z-order

**Phase** 1 · framework · **After** [FW-1](FW-1-module-skeleton-and-core-interfaces-wired-end-to.md), [SH-1](SH-1-window-gpu-rendering-hidpi-and-1-text.md) · **Unblocks** [FW-5](FW-5-interaction-states-hover-press-focus-rings-disab.md), [FW-7](FW-7-theme-tokens-layered-resolution-live-reload-the-.md), [FW-8](FW-8-headless-backend-display-list-snapshots-plus-per.md)

## Design

- `_design/architecture.md`
- `_design/rendering.md`
- `_design/shell.md`
- `_design/ui.md`
- `_design/theming.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Done when

layout golden tests; a 10k-row scroll at 120 Hz

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
