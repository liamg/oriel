---
id: ED-3
title: "Visual and visual-block, :s/:g with vim regex, ranges"
phase: P2
status: todo
area: Editor
module: vim
depends_on: [ED-2]
---

# ED-3 · Visual and visual-block, :s/:g with vim regex, ranges

**Phase** 2 · editor core · **After** [ED-2](ED-2-vim-engine-motions-operators-text-objects-regist.md) · **Unblocks** nothing yet

## Design

- `_design/editor.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Mockups

![09-lsp](../_design/mockups/09-lsp.png)

## Done when

the same, plus the regex conformance suite

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
