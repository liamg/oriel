---
id: P0-2
title: "CI on macOS and Linux: dependency allowlist, vendor check, govulncheck, refcheck, vendored spec/"
phase: P0
status: done
area: Spike and repo
module: -
depends_on: [P0-1]
---

# P0-2 · CI on macOS and Linux: dependency allowlist, vendor check, govulncheck, refcheck, vendored spec/

**Phase** 0 · spike and repo · **After** [P0-1](P0-1-repo-skeleton-go-work-module-stubs-from-architec.md) · **Unblocks** nothing yet

## Design

- `_design/deps.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Done when

As described in the design docs, with the results written up in the PR.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
