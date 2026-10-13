---
id: ED-1
title: "Rope, snapshots, anchors, authorship, undo tree"
phase: P2
status: done
area: Editor
module: text
depends_on: [FW-1]
---

# ED-1 · Rope, snapshots, anchors, authorship, undo tree

**Phase** 2 · editor core · **After** [FW-1](FW-1-module-skeleton-and-core-interfaces-wired-end-to.md) · **Unblocks** [ED-2](ED-2-vim-engine-motions-operators-text-objects-regist.md), [ED-6](ED-6-tree-sitter-highlighting-folds-outline-code-unit.md), [ED-7](ED-7-decorations-between-lines-threads-explain-cards-.md)

## Design

- `_design/editor.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Done when

property tests against a naive model; fuzzing with no failures for 1 hour

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
