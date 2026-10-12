---
id: P0-5
title: "Codex app-server billing check: subscription or per-token"
phase: P0
status: done
area: Spike and repo
module: -
depends_on: []
---

# P0-5 · Codex app-server billing check: subscription or per-token

**Phase** 0 · spike and repo · **After** nothing · **Unblocks** nothing yet

## Design

- `_design/agents.md`
- `_design/features.md` (this row)

## Done when

As described in the design docs, with the results written up in the PR.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
