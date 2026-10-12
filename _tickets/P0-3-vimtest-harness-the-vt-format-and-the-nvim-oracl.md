---
id: P0-3
title: "vimtest harness: the .vt format and the nvim oracle"
phase: P0
status: done
area: Spike and repo
module: -
depends_on: [P0-1]
---

# P0-3 · vimtest harness: the .vt format and the nvim oracle

**Phase** 0 · spike and repo · **After** [P0-1](P0-1-repo-skeleton-go-work-module-stubs-from-architec.md) · **Unblocks** [ED-2](ED-2-vim-engine-motions-operators-text-objects-regist.md)

## Design

- `_design/testing.md`
- `_design/editor.md`
- `_design/features.md` (this row)

## Done when

As described in the design docs, with the results written up in the PR.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
