---
id: SH-12
title: "Settings page (:Settings, ⌘,) and settings.toml storage"
phase: P3
status: done
area: Shell and navigation
module: app, registry
depends_on: [FW-3, FW-6, FW-11]
---

# SH-12 · Settings page (:Settings, ⌘,) and settings.toml storage

**Phase** 3 · shell · **After** [FW-3](FW-3-registries-commands-keymaps-modes-chords-panel-l.md), [FW-6](FW-6-widgets-buttons-chips-inputs-virtual-lists-trees.md), [FW-11](FW-11-toml-module-toml-1-0-parser-and-round-tripping.md) · **Unblocks** [ED-9](ED-9-editorconfig-and-per-file-editor-conventions.md)

## Design

- `_design/settings.md`
- `_design/plugins.md`, `_design/scripting.md`
- `_design/help.md`
- `_design/features.md` (this row)

## Mockups

![19-config](../_design/mockups/19-config.png)

## Done when

`settings.md` precedence tests; a GUI edit and a reset each change exactly one line of a hand-commented file; Lua-set options are read-only with their source

## As built

- settings.toml storage (user, and project behind a SHA-256 trust), live reload, the `:Settings`/`⌘,` page with
  sections, search filters, scopes, typed controls, sources, reset and undo. See settings.md, "As built (SH-12)".
- Lua isn't wired up yet; `Options.SetFrom(..., LayerLua, ..., "init.lua:12")` is what its `L.o` must call.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
