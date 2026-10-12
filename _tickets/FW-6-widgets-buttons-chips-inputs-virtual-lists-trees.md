---
id: FW-6
title: "Widgets: buttons, chips, inputs, virtual lists, trees, tabs, menus, context menus, modals, popovers, toasts"
phase: P1
status: todo
area: Framework (first, after the spike)
module: ui
depends_on: [FW-5, FW-7]
---

# FW-6 · Widgets: buttons, chips, inputs, virtual lists, trees, tabs, menus, context menus, modals, popovers, toasts

**Phase** 1 · framework · **After** [FW-5](FW-5-interaction-states-hover-press-focus-rings-disab.md), [FW-7](FW-7-theme-tokens-layered-resolution-live-reload-the-.md) · **Unblocks** [AI-4](AI-4-new-session-launcher-modes-provider-repos-worker.md), [ED-7](ED-7-decorations-between-lines-threads-explain-cards-.md), [FG-1](FG-1-explorer-scopes-changed-uncommitted-all-with-mar.md), [FW-10](FW-10-accessibility-tree-emitted-from-widget-ids.md), [FW-9](FW-9-layout-tree-and-tab-groups-drag-any-pane-to-any-.md), [SH-10](SH-10-help-everywhere-right-click-help-k-help.md), [SH-2](SH-2-session-tabs-with-status-dots-and-1-9.md), [SH-4](SH-4-k-omnibox-sessions-commands-files-symbols-ticket.md), [SH-6](SH-6-which-key-popup.md), [SH-8](SH-8-hud-drawer-hold-and-tats-in-slots.md), [SH-9](SH-9-inbox-and-interrupt-cards-agentbus-network-gatew.md), [TR-3](TR-3-tests-panel-tree-filters-flaky-detection-run-his.md)

## Design

- `_design/architecture.md`
- `_design/rendering.md`
- `_design/shell.md`
- `_design/ui.md`
- `_design/theming.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Done when

each widget has snapshot tests in dark and light, keyboard and mouse paths, and a help id

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
