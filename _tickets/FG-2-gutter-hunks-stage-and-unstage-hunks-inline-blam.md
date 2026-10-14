---
id: FG-2
title: "Gutter hunks, stage and unstage hunks, inline blame by author kind"
phase: P4
status: done
area: Files, git, terminals
module: gitx
depends_on: [ED-7]
---

# FG-2 · Gutter hunks, stage and unstage hunks, inline blame by author kind

**Phase** 4 · IDE · **After** [ED-7](ED-7-decorations-between-lines-threads-explain-cards-.md) · **Unblocks** [FG-6](FG-6-ship-page-uncommitted-behind-unpushed-pr-checks-.md), [RV-1](RV-1-split-and-unified-diff-intraline-per-hunk-review.md)

## Design

- `_design/ide.md`
- `_design/ui.md`
- `_design/features.md` (this row)

## Mockups

![06-review](../_design/mockups/06-review.png)

## Done when

staging a hunk matches `git apply --cached`

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
