---
id: SH-16
title: "The rail: main views down the left edge"
phase: P3
status: done
area: Shell and navigation
module: app
depends_on: [FW-9, SH-14]
---

# SH-16 · The rail: main views down the left edge

**Phase** 3 · shell · **After** [FW-9](FW-9-layout-tree-and-tab-groups-drag-any-pane-to-any-.md), [SH-14](SH-14-closing-views-and-real-keys-and-commands-lists.md) · **Unblocks** [FG-1](FG-1-explorer-scopes-changed-uncommitted-all-with-mar.md) (the Explorer takes its rail slot)

## Design

- `_design/ui.md` (01 · The IDE, 01f · The rail)
- `_design/plugins.md` (the rail is a contribution point)
- `_design/theming.md`
- `_design/features.md` (this row)

## Mockups

![01-ide](../_design/mockups/01-ide.png)

The icon strip on the far left only, with its badges and the settings cog at the bottom.

## What

- A **contribution point** (`registry.RailEntry`, `oriel.rail.add`): an icon (a built-in icon's name, or a glyph), a title (the tooltip, with the entry's key), an optional badge count, and
  the command it runs with its args (usually toggling a view in the left dock). Built-ins register through it like any plugin.
- **The mockup's full set from day one**, top to bottom: Explorer, Search, Git, Requests (badge), Debug, Tests;
  Settings pinned at the bottom.
  - Search (the grep picker) and Settings work now.
  - The rest are **greyed until their tickets make them available**: Explorer (FG-1), Git (FG-6), Requests (AI-5),
    Debug (DB-1), Tests (TR-3).
- **Availability is general, not a rail flag**: a command can say whether it's available for given args
  (e.g. `view.open` for a view nobody has registered isn't). The rail, the start view (SH-15) and the omnibox all
  read it the same way: grey in the rail and start view, left out of the omnibox.
- The active entry is marked; clicking it again hides the dock. Each entry is focusable and has its own key:
  Explorer `⌘⇧E`, Search `⌘⇧F`, Git `⌘⇧G`, Requests `⌘⇧R`, Debug `⌘⇧D`, Tests `⌘⇧T`, Settings `⌘,`. They're ordinary
  keymaps (rebind, unbind, override), and the tooltip shows what's bound now.

## Done when

snapshot of the rail against 01-ide's strip, with the unavailable entries grey; a plugin-registered entry appears, badges, runs and is removable;
toggling an entry shows and hides its view in the left dock

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
