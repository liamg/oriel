---
id: RV-3
title: "Cross-model AI review findings"
phase: P6
status: review
area: Review and explain
module: agent
depends_on: [RV-1, AI-3]
---

# RV-3 · Cross-model AI review findings

**Phase** 6 · review · **After** [RV-1](RV-1-split-and-unified-diff-intraline-per-hunk-review.md), [AI-3](AI-3-codex-provider-app-server-billing-checked-in-p0.md) · **Unblocks** nothing yet

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
