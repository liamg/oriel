---
id: TR-3
title: "Tests panel: tree, filters, flaky detection, run history"
phase: P4
status: todo
area: LSP, tests, debugging
module: testrun, app
depends_on: [TR-1, FW-6]
---

# TR-3 · Tests panel: tree, filters, flaky detection, run history

**Phase** 4 · IDE · **After** [TR-1](TR-1-gutter-run-for-tests-subtests-and-table-rows-inl.md), [FW-6](FW-6-widgets-buttons-chips-inputs-virtual-lists-trees.md) · **Unblocks** nothing yet

## Design

- `_design/ide.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Mockups

![36-tests](../_design/mockups/36-tests.png)

## Done when

Acceptance tests derived from the design docs above, written first, all passing in CI on macOS and Linux.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
