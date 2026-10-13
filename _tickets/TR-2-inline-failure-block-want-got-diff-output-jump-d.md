---
id: TR-2
title: "Inline failure block: want/got diff, output, jump, debug, ask to explain, ask to fix"
phase: P4
status: done
area: LSP, tests, debugging
module: testrun, app
depends_on: [TR-1]
---

# TR-2 · Inline failure block: want/got diff, output, jump, debug, ask to explain, ask to fix

**Phase** 4 · IDE · **After** [TR-1](TR-1-gutter-run-for-tests-subtests-and-table-rows-inl.md) · **Unblocks** [TR-6](TR-6-ask-to-explain-next-to-every-ask-to-fix-tests-di.md)

## Design

- `_design/ide.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Mockups

![36-tests](../_design/mockups/36-tests.png)

## Done when

a failing fixture test renders the block at the assertion line; explain returns a card without editing any file

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
