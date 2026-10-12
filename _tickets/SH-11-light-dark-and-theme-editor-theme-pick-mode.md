---
id: SH-11
title: "Light, dark and theme editor (:Theme, pick mode)"
phase: P3
status: todo
area: Shell and navigation
module: theme
depends_on: [FW-7]
---

# SH-11 · Light, dark and theme editor (:Theme, pick mode)

**Phase** 3 · shell · **After** [FW-7](FW-7-theme-tokens-layered-resolution-live-reload-the-.md) · **Unblocks** nothing yet

## Design

- `_design/theming.md`
- `_design/ui.md`
- `_design/features.md` (this row)

## Mockups

![17-light](../_design/mockups/17-light.png)
![23-theme](../_design/mockups/23-theme.png)

## Done when

token resolution tests; the fixture renders in all built-in themes; pick-to-override writes the smallest TOML diff

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
