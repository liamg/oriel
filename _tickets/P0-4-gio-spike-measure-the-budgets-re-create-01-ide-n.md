---
id: P0-4
title: "Gio spike: measure the budgets, re-create 01-ide natively, go/no-go report"
phase: P0
status: todo
area: Spike and repo
module: -
depends_on: [P0-1]
---

# P0-4 · Gio spike: measure the budgets, re-create 01-ide natively, go/no-go report

**Phase** 0 · spike and repo · **After** [P0-1](P0-1-repo-skeleton-go-work-module-stubs-from-architec.md) · **Unblocks** [SH-1](SH-1-window-gpu-rendering-hidpi-and-1-text.md)

## Design

- `_design/shell.md`
- `_design/rendering.md`
- `_design/features.md` (this row)

## Done when

As described in the design docs, with the results written up in the PR.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
