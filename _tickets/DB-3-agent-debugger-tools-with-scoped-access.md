---
id: DB-3
title: "Agent debugger tools with scoped access"
phase: P7
status: done
area: LSP, tests, debugging
module: dap, agent
depends_on: [DB-1, AI-2]
---

# DB-3 · Agent debugger tools with scoped access

**Phase** 7 · the rest · **After** [DB-1](DB-1-dap-breakpoints-conditional-logpoints-stepping-s.md), [AI-2](AI-2-claude-provider-interactive-pty-plus-mod-hooks-n.md) · **Unblocks** nothing yet

## Design

- `_design/ide.md`
- `_design/features.md` (this row)

## Mockups

![08-debug](../_design/mockups/08-debug.png)

## Done when

the scope is enforced: eval allowed, step denied when not granted

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
