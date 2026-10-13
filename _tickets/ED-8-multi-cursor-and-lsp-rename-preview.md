---
id: ED-8
title: "Multi-cursor and LSP rename preview"
phase: P4
status: done
area: Editor
module: edit
depends_on: [ED-7, LS-1]
---

# ED-8 · Multi-cursor and LSP rename preview

**Phase** 4 · IDE · **After** [ED-7](ED-7-decorations-between-lines-threads-explain-cards-.md), [LS-1](LS-1-completion-hover-signature-help-diagnostics-code.md) · **Unblocks** nothing yet

## Design

- `_design/editor.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Mockups

![09-lsp](../_design/mockups/09-lsp.png)

## Done when

Acceptance tests derived from the design docs above, written first, all passing in CI on macOS and Linux.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
