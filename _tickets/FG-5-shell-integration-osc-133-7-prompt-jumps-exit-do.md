---
id: FG-5
title: "Shell integration (OSC 133/7): prompt jumps, exit dots, send output to a request"
phase: P4
status: todo
area: Files, git, terminals
module: term
depends_on: [FG-4]
---

# FG-5 · Shell integration (OSC 133/7): prompt jumps, exit dots, send output to a request

**Phase** 4 · IDE · **After** [FG-4](FG-4-terminals-ghostty-vt-under-wazero-our-renderer-p.md) · **Unblocks** nothing yet

## Design

- `_design/ide.md`
- `_design/ui.md`
- `_design/features.md` (this row)

## Mockups

![24-layout](../_design/mockups/24-layout.png)

## Done when

Acceptance tests derived from the design docs above, written first, all passing in CI on macOS and Linux.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
