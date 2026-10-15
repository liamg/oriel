---
id: SE-3
title: "Files outside the session open read-only; app opens on mission control"
phase: P5
status: done
area: Sessions and AI
module: app
depends_on: [SE-1]
---

# SE-3 · Files outside the session open read-only; app opens on mission control

**Phase** 5 · AI · **After** [SE-1](SE-1-session-kinds-plan-work-review-tour-job-new-repo.md) · **Unblocks** nothing yet

## Design

- `_design/sessions.md`
- `_design/architecture.md`
- `_design/features.md` (this row)

## Mockups

![01-ide](../_design/mockups/01-ide.png)

## Done when

Acceptance tests derived from the design docs above, written first, all passing in CI on macOS and Linux.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
