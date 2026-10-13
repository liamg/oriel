---
id: FW-13
title: "UI motion polish: subtle animations for panels, menus, popups and toasts"
phase: P3
status: todo
area: Framework
module: ui, ui/widget, ui/layout
depends_on: [FW-5, FW-6, FW-9]
---

# FW-13 · UI motion polish: subtle animations for panels, menus, popups and toasts

**Phase** 3 · shell · **After** [FW-5](FW-5-interaction-states-hover-press-focus-rings-disab.md), [FW-6](FW-6-widgets-buttons-chips-inputs-virtual-lists-trees.md), [FW-9](FW-9-layout-tree-and-tab-groups-drag-any-pane-to-any-.md) · **Unblocks** nothing yet

## Design

- `_design/rendering.md` (Motion, Springs: "used sparingly")
- `_design/ui.md` ("no motion that isn't telling you something")
- `_design/theming.md` (effects)
- `_design/features.md` (this row)

## What

Small, quick motion that says where something came from and where it went. Nothing bouncy, nothing slow.

- **Docks and panels**: opening and closing a dock slides it in from its edge (and fades its content); toggling
  `⌃J` animates the size, not a jump. Resizing by drag stays immediate.
- **Menus, context menus and popovers**: fade and scale in slightly from their anchor (about 0.97 → 1), fade out on
  close. Submenus the same from their parent row.
- **Modals**: the scrim fades, the card fades and rises a few pixels.
- **Toasts**: slide in from the right edge, fade out when they go; the stack below glides up to close the gap.
- **Tabs and selections**: the active tab's underline glides to the new tab; a list's selected row highlight glides
  between rows when moved by keys.
- **Hover and press**: colour changes ease over a few frames instead of switching.
- One shared set of durations and curves (the existing springs: settled by ~200 ms for small things), as theme
  effects a theme or Lua can tune, and `ui.reduced_motion` turns all of it off.
- Closing animations need the node to stay drawn after its owner stops building it: add that to `ui` generally
  (an "exit" for a keyed node), not per widget.

## Done when

- Each animation above is a spring on the existing animator, and a test steps the clock through it: the start,
  a midpoint and the settled frame are display-list snapshots.
- With `ui.reduced_motion` on, every one of those snapshots jumps straight to its settled frame.
- Nothing animates for longer than 250 ms, and nothing delays input: a key pressed mid-animation acts at once.
- The frame budgets in `rendering.md` still hold with several things animating (benchmark).

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
