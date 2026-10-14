---
id: LS-3
title: "Agent LSP tools over MCP"
phase: P5
status: review
area: LSP, tests, debugging
module: lsp, agent
depends_on: [LS-1, AI-2]
---

# LS-3 · Agent LSP tools over MCP

**Phase** 5 · AI · **After** [LS-1](LS-1-completion-hover-signature-help-diagnostics-code.md), [AI-2](AI-2-claude-provider-interactive-pty-plus-mod-hooks-n.md) · **Unblocks** nothing yet

## Design

- `_design/ide.md`
- `_design/features.md` (this row)

## Notes

- We might want to use [owenrumney/go-lsp](https://github.com/owenrumney/go-lsp) for the LSP 3.17 protocol types (its
  `lsp` package, stdlib only). It is a server framework, and its JSON-RPC layer is `internal/` and server-side. A client
  is being added upstream and may have landed by now: check before writing our own client transport. A new
  dependency: ask first (`_design/deps.md`).

## Done when

agents can call definition/references/diagnostics read-only

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
