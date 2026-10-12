---
id: ED-5
title: "plain engine and the input.Engine interface (emacs as a plugin)"
phase: P3
status: todo
area: Editor
module: input
depends_on: [ED-2, FW-3]
---

# ED-5 · plain engine and the input.Engine interface (emacs as a plugin)

**Phase** 3 · shell · **After** [ED-2](ED-2-vim-engine-motions-operators-text-objects-regist.md), [FW-3](FW-3-registries-commands-keymaps-modes-chords-panel-l.md) · **Unblocks** nothing yet

## Design

- `_design/editor.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Done when

an engine swap at runtime per buffer; a sample emacs plugin passes its tests

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
