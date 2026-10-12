---
id: PL-10
title: "Calendar: next meeting tat (iCal URL or macOS EventKit)"
phase: P7
status: todo
area: Platform
module: integ/ical (plugin)
depends_on: [PL-2]
---

# PL-10 · Calendar: next meeting tat (iCal URL or macOS EventKit)

**Phase** 7 · the rest · **After** [PL-2](PL-2-process-plugins-with-an-os-sandbox-wasm-plugins-.md) · **Unblocks** nothing yet

## Design

- `_design/crit-reference.md`
- `_design/plugins.md`
- `_design/ui.md`
- `_design/features.md` (this row)

## Mockups

![10-hud](../_design/mockups/10-hud.png)

## Done when

Acceptance tests derived from the design docs above, written first, all passing in CI on macOS and Linux.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
