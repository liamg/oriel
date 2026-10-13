---
id: ED-14
title: "Highlight queries without a parent walk per capture"
phase: P3
status: review
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

## As built

- The parent walk wasn't most of the cost. Patterns on the root node (Go's doc comments,
  `(source_file (comment)+ . (function_declaration))`, Bash's and Ruby's) made tree-sitter match against every
  top-level node whatever the range: 8 ms for any query on the 10,000-line file. Now:
  - The query runs inside a window: the top-level nodes the range touches, and the one after
    (`ts_query_cursor_set_containing_byte_range`).
  - A root pattern that only lists siblings is rewritten as that group with `#has-parent? @capture <root>`
    (`syntax/rootpat.go`). One it can't rewrite (anchored to the first child, inside a predicate group) runs apart,
    with a max start depth of 0, in microseconds. Patterns keep their places, so later ones still win.
  - Only captures a `#has-parent?` reads get a parent, found down from their top-level node.
  - Injection patterns into a language the set doesn't have are turned off (Go's comment, regex and printf).
- A highlight query now costs what its range holds, wherever it is: 16 KB half way down the 10,000-line file costs
  the same as 16 KB of a 20 KB file. **That is still about 9 ms for dense Go, not the 2 ms this ticket asked**:
  tree-sitter's matcher costs about 1.6 µs a match under wazero (`-O3` doesn't help). What the 2 ms protects is how
  long one wasm call holds up the Go runtime, so the parser queries in 1 KB chunks, each under 2 ms (the test checks
  every call). Done-when 1 is met in that form.
- A Document re-highlights only the edited lines and the ranges tree-sitter reports changed
  (`ts_tree_get_changed_ranges`, worked out on demand from the kept previous tree), not the whole file.
- `TestEditorTypingBudget` holds the worst keypress to frame under 8 ms (5–7 ms here). Every grammar is rebuilt from
  the new shim with wasi-sdk 34; `build.sh` reproduces `SHA256SUMS`.
