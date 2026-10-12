---
id: RV-6
title: "Explain this change walkthrough"
phase: P6
status: todo
area: Review and explain
module: explain
depends_on: [RV-5]
---

# RV-6 · Explain this change walkthrough

**Phase** 6 · review · **After** [RV-5](RV-5-explain-by-code-unit-widen-and-narrow-explain-th.md) · **Unblocks** nothing yet

## Design

- `_design/ui.md`
- `_design/agents.md`
- `_design/features.md` (this row)

## Mockups

![07-explain](../_design/mockups/07-explain.png)

## Done when

Acceptance tests derived from the design docs above, written first, all passing in CI on macOS and Linux.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
