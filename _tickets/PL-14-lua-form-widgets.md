---
id: PL-14
title: "Form widgets from Lua: inputs, toggles, selects and a view's own state"
phase: P4
status: todo
area: Platform
module: script/api, app
depends_on: [PL-1]
---

# PL-14 · Form widgets from Lua: inputs, toggles, selects and a view's own state

**Phase** 4 · **After** PL-1 · **Unblocks** nothing yet

Asked for in P3, after the plugin UI demo (`app/plugindemo_test.go`) showed what plugins can and can't do yet.

## Design

- `_design/scripting.md` (UI from Lua)
- `_design/plugins.md` (UI from extensions)
- `_design/features.md` (this row)

## What

Lua views can show text, rows, tats, lists and buttons, but can't take input. Add the controls a small panel
needs, on the same widgets the built-ins use, so they look and behave the same (focus, keys, help ids).

- `oriel.ui.input{ value, placeholder, on_change, on_submit }`, `oriel.ui.toggle{ on, on_change }`,
  `oriel.ui.select{ value, options, on_change }`.
- State that lives across redraws without globals: `oriel.ui.state(key, initial)` keyed by the view instance.
- Errors in a callback go through PL-12's path (notice, diagnostic, three in a row turn the view off).

## Done when

- A Lua panel with an input, a toggle and a select: typing, toggling and choosing call the plugin's functions, and the panel redraws from its state.
- Tab moves focus through the controls; Enter submits the input; the controls carry help ids that resolve.
- A callback that errors shows as a PL-12 notice with its `init.lua:N` line.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
