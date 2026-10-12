---
id: LS-2
title: "Definition, references peek, rename, symbols, call hierarchy"
phase: P4
status: todo
area: LSP, tests, debugging
module: lsp
depends_on: [LS-1]
---

# LS-2 · Definition, references peek, rename, symbols, call hierarchy

**Phase** 4 · IDE · **After** [LS-1](LS-1-completion-hover-signature-help-diagnostics-code.md) · **Unblocks** nothing yet

## Design

- `_design/ide.md`
- `_design/features.md` (this row)

## Mockups

![09-lsp](../_design/mockups/09-lsp.png)

## Done when

the same

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
