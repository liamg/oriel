---
id: PL-11
title: "Prompts page (editable prompt packs, coding style written into CLAUDE.md), voice dictation"
phase: P7
status: todo
area: Platform
module: prompts, voice
depends_on: [AI-2]
---

# PL-11 · Prompts page (editable prompt packs, coding style written into CLAUDE.md), voice dictation

**Phase** 7 · the rest · **After** [AI-2](AI-2-claude-provider-interactive-pty-plus-mod-hooks-n.md) · **Unblocks** nothing yet

## Design

- `_design/crit-reference.md`
- `_design/agents.md`
- `_design/features.md` (this row)

## Done when

`crit-reference.md` → Prompts and Voice

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
