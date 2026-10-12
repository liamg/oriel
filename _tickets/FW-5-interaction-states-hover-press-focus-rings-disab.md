---
id: FW-5
title: "Interaction states: hover, press, focus rings, disabled, cursors, tooltips; spring animation"
phase: P1
status: todo
area: Framework (first, after the spike)
module: ui
depends_on: [FW-4]
---

# FW-5 · Interaction states: hover, press, focus rings, disabled, cursors, tooltips; spring animation

**Phase** 1 · framework · **After** [FW-4](FW-4-ui-toolkit-core-ctx-ids-flex-layout-anchors-scro.md) · **Unblocks** [FW-6](FW-6-widgets-buttons-chips-inputs-virtual-lists-trees.md)

## Design

- `_design/architecture.md`
- `_design/rendering.md`
- `_design/shell.md`
- `_design/ui.md`
- `_design/theming.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Done when

display-list snapshots per state; 200 hover targets stay within the frame budget; reduced-motion respected

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
