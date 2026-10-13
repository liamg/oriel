---
id: PL-15
title: "Drag and drop for plugins: drag sources, drop targets, and files dropped on the window"
phase: P4
status: todo
area: Platform
module: ui, registry, script/api, app
depends_on: [FW-9, PL-1]
---

# PL-15 · Drag and drop for plugins: drag sources, drop targets, and files dropped on the window

**Phase** 4 · **After** FW-9, PL-1 · **Unblocks** nothing yet

Asked for in P3, after the plugin UI demo (`app/plugindemo_test.go`) showed what plugins can and can't do yet.

## Design

- `_design/ui.md` (layout, dragging tabs)
- `_design/plugins.md` (UI from extensions)
- `_design/features.md` (this row)

## What

Tabs drag between groups (FW-9), but nothing else can be dragged, and a plugin can't take a drop. Make it general:

- `ui`: a box can be a drag source (`Style.Drag` with a payload of a kind and data) and a drop target (`Style.Drop`
  with the kinds it takes), with the hover glow the layout's drop targets already use.
- Payload kinds: `file` (a path, line), `text`, and plugin-defined ones (`myplugin.ticket`).
- Files dropped on the window from the OS arrive as `file` payloads (Gio's transfer events), opening in the editor
  by default and offered to any target under the pointer first.
- Lua binds both on `oriel.ui` nodes: `drag = { kind, data }`, `on_drop = function(kind, data) … end`.

## Done when

- A Lua panel row dragged onto another Lua panel's target calls its `on_drop` with the payload; a target that doesn't take the kind doesn't glow or receive it.
- Dragging a file from the explorer (or the OS) onto the editor opens it; onto a plugin target that takes `file`, the plugin gets the path.
- Drags work by keyboard too (pick up, move focus, drop), for accessibility.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
