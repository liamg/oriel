---
id: RV-4
title: "GitHub review mirroring and Forward to GitHub"
phase: P6
status: todo
area: Review and explain
module: gh
depends_on: [RV-1, FG-6]
---

# RV-4 · GitHub review mirroring and Forward to GitHub

**Phase** 6 · review · **After** [RV-1](RV-1-split-and-unified-diff-intraline-per-hunk-review.md), [FG-6](FG-6-ship-page-uncommitted-behind-unpushed-pr-checks-.md) · **Unblocks** nothing yet

## Design

- `_design/crit-reference.md`
- `_design/agents.md`
- `_design/ui.md`
- `_design/features.md` (this row)

## Mockups

![06-review](../_design/mockups/06-review.png)

## Done when

the agent never posts; the user's forward does

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
