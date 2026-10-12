---
id: AI-10
title: "Commit and PR drafts when the agent goes idle"
phase: P6
status: todo
area: Sessions and AI
module: agent
depends_on: [AI-2, FG-6]
---

# AI-10 · Commit and PR drafts when the agent goes idle

**Phase** 6 · review · **After** [AI-2](AI-2-claude-provider-interactive-pty-plus-mod-hooks-n.md), [FG-6](FG-6-ship-page-uncommitted-behind-unpushed-pr-checks-.md) · **Unblocks** nothing yet

## Design

- `_design/agents.md`
- `_design/crit-reference.md`
- `_design/features.md` (this row)

## Mockups

![18-git](../_design/mockups/18-git.png)

## Done when

Acceptance tests derived from the design docs above, written first, all passing in CI on macOS and Linux.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
