---
id: FW-2
title: "Event store: typed events, materialised core.State, subscriptions, replay"
phase: P1
status: done
area: Framework (first, after the spike)
module: core
depends_on: [FW-1]
---

# FW-2 · Event store: typed events, materialised core.State, subscriptions, replay

**Phase** 1 · framework · **After** [FW-1](FW-1-module-skeleton-and-core-interfaces-wired-end-to.md) · **Unblocks** [AI-1](AI-1-worker-wire-sessions-workspaces-with-multi-repo-.md), [SH-2](SH-2-session-tabs-with-status-dots-and-1-9.md), [SH-8](SH-8-hud-drawer-hold-and-tats-in-slots.md)

## Design

- `_design/architecture.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Done when

replaying a recorded event log reproduces the state byte-for-byte

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
