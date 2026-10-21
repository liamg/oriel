---
id: FW-15
title: "Motion polish, round two: hover, focus, scrolls, controls and the editor cursor"
phase: P3
status: review
area: Framework
module: ui, ui/widget, app
depends_on: [FW-13]
---

# FW-15 · Motion polish, round two: hover, focus, scrolls, controls and the editor cursor

**Phase** 3 · shell · **After** [FW-13](FW-13-ui-motion-polish-subtle-animations-for-panels-menus-and-popups.md) · **Unblocks** nothing yet

## Design

- `_design/rendering.md` ("More motion")
- `_design/ui.md` ("no motion that isn't telling you something")

## What

Small, quick flourishes on what's touched most, on FW-13's spring. Nothing bouncy, slow or looping.

- **Toolkit**: `Ctx.Ease` and `Ctx.Snap`; the hover and press wash eases; `Style.Sink`; the focus ring settles in
  and glides between siblings; tooltips fade and rise; auto scrollbars fade in; `ScrollTo` glides.
- **Widgets**: toggle, check, Segmented, Choices, Tree, HoverCard, Meter, Ring, ProgressBar, Badge, Page and busy
  buttons.
- **Editor**: the cursor glides between places (`editor.cursor_glide`, on by default). A step of one column on the
  same line, a move within 90 ms of the last and a scroll snap, so it never trails typing or held keys.

## Done when

- Each animation is stepped through by a test: the start, a midpoint and the settled frame are display-list snapshots.
- With `ui.reduced_motion` on, each starts where it settles.
- Nothing animates for longer than 250 ms, and nothing delays input.
- The cursor moves at once for typing, single steps, quick runs and with the option off.
- The frame budgets still hold.

## As built

- Snapshots: `ui/testdata/motion/toolkit-*`, `ui/widget/testdata/motion`; `TestScrollToGlides`, `TestCursorGlide`.
- Differs from the plan: a tree's closed rows go at once (they're rows of a virtual list, so they can't stay drawn
  on their way out); other engines' cursors don't glide yet; a notched wheel still scrolls a line at a time at once
  (the wheel stays direct, as a trackpad's does).
