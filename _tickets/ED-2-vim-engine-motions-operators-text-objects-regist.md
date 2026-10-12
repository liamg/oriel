---
id: ED-2
title: "Vim engine: motions, operators, text objects, registers, marks, dot, counts"
phase: P2
status: todo
area: Editor
module: vim
depends_on: [ED-1, P0-3]
---

# ED-2 · Vim engine: motions, operators, text objects, registers, marks, dot, counts

**Phase** 2 · editor core · **After** [ED-1](ED-1-rope-snapshots-anchors-authorship-undo-tree.md), [P0-3](P0-3-vimtest-harness-the-vt-format-and-the-nvim-oracl.md) · **Unblocks** [ED-3](ED-3-visual-and-visual-block-s-g-with-vim-regex-range.md), [ED-4](ED-4-macros-folds-splits-buffers-quickfix.md), [ED-5](ED-5-plain-engine-and-the-input-engine-interface-emac.md)

## Design

- `_design/editor.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Mockups

![01-ide](../_design/mockups/01-ide.png)

## Done when

the `.vt` suite passes, oracle mode passes, mined vim tests above 90% passing

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
