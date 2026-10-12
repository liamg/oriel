---
id: FW-8
title: "Headless backend: display-list snapshots plus perceptual PNG diffs against the mockups"
phase: P1
status: done
area: Framework (first, after the spike)
module: render
depends_on: [FW-4]
---

# FW-8 · Headless backend: display-list snapshots plus perceptual PNG diffs against the mockups

**Phase** 1 · framework · **After** [FW-4](FW-4-ui-toolkit-core-ctx-ids-flex-layout-anchors-scro.md) · **Unblocks** [FW-9](FW-9-layout-tree-and-tab-groups-drag-any-pane-to-any-.md)

## Design

- `_design/rendering.md`
- `_design/testing.md`
- `_design/shell.md`
- `_design/features.md` (this row)

## Done when

CI renders the fixture workspace and diffs it within threshold

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
