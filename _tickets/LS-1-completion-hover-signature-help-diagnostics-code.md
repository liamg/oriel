---
id: LS-1
title: "Completion, hover, signature help, diagnostics, code actions, inlay hints"
phase: P4
status: done
area: LSP, tests, debugging
module: lsp
depends_on: [ED-7]
---

# LS-1 · Completion, hover, signature help, diagnostics, code actions, inlay hints

**Phase** 4 · IDE · **After** [ED-7](ED-7-decorations-between-lines-threads-explain-cards-.md) · **Unblocks** [ED-8](ED-8-multi-cursor-and-lsp-rename-preview.md), [LS-2](LS-2-definition-references-peek-rename-symbols-call-h.md), [LS-3](LS-3-agent-lsp-tools-over-mcp.md)

## Design

- `_design/ide.md`
- `_design/features.md` (this row)

## Mockups

![09-lsp](../_design/mockups/09-lsp.png)

## Notes

- We might want to use [owenrumney/go-lsp](https://github.com/owenrumney/go-lsp) for the LSP 3.17 protocol types (its
  `lsp` package, stdlib only). It is a server framework, and its JSON-RPC layer is `internal/` and server-side. A client
  is being added upstream and may have landed by now: check before writing our own client transport. A new
  dependency: ask first (`_design/deps.md`).

## Done when

recorded gopls/tsserver/pyright transcripts replay to the expected UI state

## From SH-13

- Capture the child's stderr with `logs.Lines` (a `slog` source per server or plugin), so it shows in the log panel (`errors.md`).

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
