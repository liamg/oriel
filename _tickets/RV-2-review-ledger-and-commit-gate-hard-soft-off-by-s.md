---
id: RV-2
title: "Review ledger and commit gate (hard, soft, off by session kind)"
phase: P6
status: review
area: Review and explain
module: core
depends_on: [RV-1]
---

# RV-2 · Review ledger and commit gate (hard, soft, off by session kind)

**Phase** 6 · review · **After** [RV-1](RV-1-split-and-unified-diff-intraline-per-hunk-review.md) · **Unblocks** nothing yet

## Design

- `_design/ui.md`
- `_design/agents.md`
- `_design/features.md` (this row)

## Mockups

![06-review](../_design/mockups/06-review.png)
![18-git](../_design/mockups/18-git.png)

## Done when

the gate blocks; "commit anyway" writes the trailer

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
