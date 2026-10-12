---
id: RV-1
title: "Split and unified diff, intraline, per-hunk reviewed"
phase: P6
status: todo
area: Review and explain
module: app, diff
depends_on: [ED-7, FG-2]
---

# RV-1 · Split and unified diff, intraline, per-hunk reviewed

**Phase** 6 · review · **After** [ED-7](ED-7-decorations-between-lines-threads-explain-cards-.md), [FG-2](FG-2-gutter-hunks-stage-and-unstage-hunks-inline-blam.md) · **Unblocks** [RV-2](RV-2-review-ledger-and-commit-gate-hard-soft-off-by-s.md), [RV-3](RV-3-cross-model-ai-review-findings.md), [RV-4](RV-4-github-review-mirroring-and-forward-to-github.md)

## Design

- `_design/ui.md`
- `_design/agents.md`
- `_design/features.md` (this row)

## Mockups

![06-review](../_design/mockups/06-review.png)

## Done when

Acceptance tests derived from the design docs above, written first, all passing in CI on macOS and Linux.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
