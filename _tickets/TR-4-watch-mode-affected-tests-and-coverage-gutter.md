---
id: TR-4
title: "Watch mode (affected tests) and coverage gutter"
phase: P4
status: done
area: LSP, tests, debugging
module: testrun
depends_on: [TR-1]
---

# TR-4 · Watch mode (affected tests) and coverage gutter

**Phase** 4 · IDE · **After** [TR-1](TR-1-gutter-run-for-tests-subtests-and-table-rows-inl.md) · **Unblocks** nothing yet

## Design

- `_design/ide.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Mockups

![36-tests](../_design/mockups/36-tests.png)

## Done when

a save re-runs only the affected packages in the Go fixture

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
