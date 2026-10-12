---
id: LS-3
title: "Agent LSP tools over MCP"
phase: P5
status: todo
area: LSP, tests, debugging
module: lsp, agent
depends_on: [LS-1, AI-2]
---

# LS-3 · Agent LSP tools over MCP

**Phase** 5 · AI · **After** [LS-1](LS-1-completion-hover-signature-help-diagnostics-code.md), [AI-2](AI-2-claude-provider-interactive-pty-plus-mod-hooks-n.md) · **Unblocks** nothing yet

## Design

- `_design/ide.md`
- `_design/features.md` (this row)

## Done when

agents can call definition/references/diagnostics read-only

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
