---
id: P0-1
title: "Repo skeleton: go.work, module stubs from architecture.md"
phase: P0
status: todo
area: Spike and repo
module: -
depends_on: []
---

# P0-1 · Repo skeleton: go.work, module stubs from architecture.md

**Phase** 0 · spike and repo · **After** nothing · **Unblocks** [FW-1](FW-1-module-skeleton-and-core-interfaces-wired-end-to.md), [P0-2](P0-2-ci-on-macos-and-linux-dependency-allowlist-vendo.md), [P0-3](P0-3-vimtest-harness-the-vt-format-and-the-nvim-oracl.md), [P0-4](P0-4-gio-spike-measure-the-budgets-re-create-01-ide-n.md)

## Design

- `_design/architecture.md`
- `_design/features.md` (this row)

## Done when

As described in the design docs, with the results written up in the PR.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
