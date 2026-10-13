---
id: FW-10
title: "Accessibility tree emitted from widget ids"
phase: P1
status: done
area: Framework (first, after the spike)
module: ui
depends_on: [FW-6]
---

# FW-10 · Accessibility tree emitted from widget ids

**Phase** 1 · framework · **After** [FW-6](FW-6-widgets-buttons-chips-inputs-virtual-lists-trees.md) · **Unblocks** nothing yet

## Design

- `_design/architecture.md`
- `_design/rendering.md`
- `_design/shell.md`
- `_design/ui.md`
- `_design/theming.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Done when

the fixture's accessibility tree, emitted from widget ids, has its main regions with roles and names. A screen reader
reading it needs the platform bridges, which are [FW-12](FW-12-platform-accessibility-bridges-nsaccessibility-and-at-spi.md)

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
