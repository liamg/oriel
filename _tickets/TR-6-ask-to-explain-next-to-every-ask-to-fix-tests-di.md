---
id: TR-6
title: ""Ask to explain" next to every "ask to fix" (tests, diagnostics, CI, conflicts, failed terminal commands)"
phase: P6
status: done
area: LSP, tests, debugging
module: explain
depends_on: [TR-2, RV-5]
---

# TR-6 · "Ask to explain" next to every "ask to fix" (tests, diagnostics, CI, conflicts, failed terminal commands)

**Phase** 6 · review · **After** [TR-2](TR-2-inline-failure-block-want-got-diff-output-jump-d.md), [RV-5](RV-5-explain-by-code-unit-widen-and-narrow-explain-th.md) · **Unblocks** nothing yet

## Design

- `_design/ide.md`
- `_design/ui.md`
- `_design/features.md` (this row)

## Mockups

![36-tests](../_design/mockups/36-tests.png)
![09-lsp](../_design/mockups/09-lsp.png)
![18-git](../_design/mockups/18-git.png)

## Done when

each fix entry point has an explain twin; explain runs tool-less

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
