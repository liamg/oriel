---
id: ED-4
title: "Macros, folds, splits, buffers, quickfix"
phase: P2
status: review
area: Editor
module: vim
depends_on: [ED-2, FW-9]
---

# ED-4 · Macros, folds, splits, buffers, quickfix

**Phase** 2 · editor core · **After** [ED-2](ED-2-vim-engine-motions-operators-text-objects-regist.md), [FW-9](FW-9-layout-tree-and-tab-groups-drag-any-pane-to-any-.md) · **Unblocks** nothing yet

## Design

- `_design/editor.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Done when

the same

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
