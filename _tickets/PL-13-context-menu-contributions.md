---
id: PL-13
title: "Context menu entries from plugins: a contribution point for right-click menus"
phase: P3
status: review
area: Platform
module: app, registry, script/api
depends_on: [PL-1, SH-10]
---

# PL-13 · Context menu entries from plugins: a contribution point for right-click menus

**Phase** 3 · **After** PL-1, SH-10 · **Unblocks** nothing yet

Asked for in P3, after the plugin UI demo (`app/plugindemo_test.go`) showed what plugins can and can't do yet.

## Design

- `_design/plugins.md` (contribution points)
- `_design/ui.md` (context menus)
- `_design/features.md` (this row)

## What

Right-click menus offer only **Help** today, and only the built-ins can add to them. A plugin should be able to add
entries to the menu of any surface, the same way the built-ins do.

- A `registry.MenuItem` contribution: a label, a command (or a Go func), an optional key shown beside it, a group
  and order, and a `When` that says where it applies: a view id, a help id prefix, a file pattern, the editor with a
  selection.
- The built-ins move onto it: the editor's own entries, the tab menu, Help as the last item.
- Lua binds it as `oriel.menu.add{ label, run, when = { view = …, file = … }, group }`; LuaLS types and `docs/api.md`
  follow from the annotations.

## Done when

- A Lua plugin adds an entry to the editor's right-click menu for `*.go` files only; it shows there, not in a `.md` file or the Problems view, and runs its function.
- The built-in entries come from the same contribution point (a test lists them through `Contributions("menu.item")`).
- Entries keep their group order, with Help last, and a disposed plugin's entries go.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.

## As built

- `registry.MenuItem` / `MenuWhen` / `MenuCtx` / `MenuItems` and `registry.Located`; `layout.Root.Visible`. The
  window's context menu is the built-in `menus` extension (it replaced the help view's Help-only menu); its own
  entries are contributions. `widget.ContextResult.Open` says whether a menu is still showing.
- Lua: `oriel.menu.add`. Tests: `app/menu_test.go`.
