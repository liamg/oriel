---
id: AI-6
title: "Agent console: structured transcript, subagents, composer"
phase: P5
status: done
area: Sessions and AI
module: app
depends_on: [AI-2]
---

# AI-6 · Agent console: structured transcript, subagents, composer

**Phase** 5 · AI · **After** [AI-2](AI-2-claude-provider-interactive-pty-plus-mod-hooks-n.md) · **Unblocks** nothing yet

## Design

- `_design/agents.md`
- `_design/sessions.md`
- `_design/architecture.md`
- `_design/ui.md`
- `_design/features.md` (this row)

## Mockups

![15-agent](../_design/mockups/15-agent.png)

## Done when

Acceptance tests derived from the design docs above, written first, all passing in CI on macOS and Linux.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
