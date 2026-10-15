---
id: PL-9
title: "Slack PTAL via webhook"
phase: P7
status: done
area: Platform
module: integ/slack (plugin)
depends_on: [PL-2]
---

# PL-9 · Slack PTAL via webhook

**Phase** 7 · the rest · **After** [PL-2](PL-2-process-plugins-with-an-os-sandbox-wasm-plugins-.md) · **Unblocks** nothing yet

## Design

- `_design/crit-reference.md`
- `_design/plugins.md`
- `_design/security.md`
- `_design/features.md` (this row)

## Mockups

![18-git](../_design/mockups/18-git.png)

## Done when

Acceptance tests derived from the design docs above, written first, all passing in CI on macOS and Linux.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
