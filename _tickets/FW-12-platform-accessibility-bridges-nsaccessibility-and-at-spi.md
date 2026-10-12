---
id: FW-12
title: "Platform accessibility bridges: NSAccessibility (macOS) and AT-SPI (Linux)"
phase: P1
status: todo
area: Framework (first, after the spike)
module: render/gio
depends_on: [FW-10]
---

# FW-12 · Platform accessibility bridges: NSAccessibility (macOS) and AT-SPI (Linux)

**Phase** 1 · framework · **After** [FW-10](FW-10-accessibility-tree-emitted-from-widget-ids.md) · **Unblocks** FW-10's done-when

## Design

- `_design/rendering.md` (Accessibility)
- `_design/deps.md`

## Done when

`render/gio`'s window implements `render.Accessible`: VoiceOver reads the fixture's main regions and can press a
button (scripted manual check on macOS), and Orca does the same on Linux.

## Notes

- **Deferred** (Liam, 2026-10-12): phase 1 closes without it, and FW-10 waits in review for it.
- The plan, no new dependency either way: on macOS, our own `NSAccessibilityElement`s set as the children of Gio's
  `NSView` (from `app.AppKitViewEvent`), through cgo in `render/gio` under `darwin` (Gio needs cgo there already); on
  Linux, pure Go: an AT-SPI tree over D-Bus with our own small D-Bus client on the standard library.

- Gio has no accessibility support, so the bridge reaches the platform itself: either AccessKit's C library through
  cgo (a new dependency: ask first, `deps.md`), or our own NSAccessibility elements on Gio's NSView (Objective-C
  through cgo) and an AT-SPI tree over D-Bus.
- It can only be checked on real desktops, with a screen reader running.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
