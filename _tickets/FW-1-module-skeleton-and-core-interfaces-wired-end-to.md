---
id: FW-1
title: "Module skeleton and core interfaces wired end to end: app → core.Store → registry → a view"
phase: P1
status: done
area: Framework (first, after the spike)
module: all
depends_on: [P0-1]
---

# FW-1 · Module skeleton and core interfaces wired end to end: app → core.Store → registry → a view

**Phase** 1 · framework · **After** [P0-1](P0-1-repo-skeleton-go-work-module-stubs-from-architec.md) · **Unblocks** [ED-1](ED-1-rope-snapshots-anchors-authorship-undo-tree.md), [FW-2](FW-2-event-store-typed-events-materialised-core-state.md), [FW-3](FW-3-registries-commands-keymaps-modes-chords-panel-l.md), [FW-4](FW-4-ui-toolkit-core-ctx-ids-flex-layout-anchors-scro.md)

## Design

- `_design/architecture.md`
- `_design/rendering.md`
- `_design/shell.md`
- `_design/ui.md`
- `_design/theming.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Done when

an empty app boots; a registered command runs from a keymap and an event updates a view; an import-rule test fails if `text`/`vim`/`core`/`agent` import `ui` or `render`

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
