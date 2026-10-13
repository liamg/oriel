---
id: ED-14
title: "Highlight queries without a parent walk per capture"
phase: P3
status: todo
area: Editor
module: syntax
depends_on: [ED-6]
---

# ED-14 · Highlight queries without a parent walk per capture

**Phase** 3 · **After** [ED-6](ED-6-tree-sitter-highlighting-folds-outline-code-unit.md) · **Unblocks** nothing yet

## Design

- `_design/editor.md`, `_design/rendering.md` (the 8 ms keypress-to-frame budget)
- `_design/deps.md` (grammars are rebuilt from pinned sources and must match what's committed)

## What

- `shim.c`'s `query_matches` calls `ts_node_parent()` for every capture, and tree-sitter finds a parent by walking
  down from the root. In a file with many top-level declarations each highlight query costs ~24 ms whatever its
  range (measured on a 10,000-line Go file), and compiled wasm can't be preempted, so the Go runtime's
  stop-the-world waits for it: typing stalls for up to ~25 ms while the background parser re-highlights.
- Only `#has-parent?` and `#not-has-parent?` need the parent. Give the parent only to captures those predicates
  read (or find it from the query cursor's path), rebuild every grammar with wasi-sdk 34, and commit the wasm.
- Re-highlight only what an edit changed (tree-sitter's changed ranges), not the whole document after each edit.

## Done when

- A highlight query over 16 KB of the 10,000-line fixture takes under 2 ms.
- `TestEditorTypingBudget`'s worst keypress to frame is under 8 ms, not only its mean.
- The grammars job rebuilds the committed wasm byte for byte.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
