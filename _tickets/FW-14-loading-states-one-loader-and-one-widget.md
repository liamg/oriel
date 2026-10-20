---
id: FW-14
title: "Loading states: one loader and one widget, used by every screen"
phase: P3
status: done
area: Framework
module: plugin, ui/widget, app, script/api
depends_on: [FW-6, FW-13, AI-4]
---

# FW-14 · Loading states: one loader and one widget, used by every screen

**Phase** 3 · shell · **After** [FW-6](FW-6-widgets-buttons-chips-inputs-virtual-lists-trees.md), [FW-13](FW-13-ui-motion-polish-subtle-animations-for-panels-menus-and-popups.md), [AI-4](AI-4-new-session-launcher-modes-provider-repos-worker.md)

## Design

- `_design/ui.md` ("Loading": the policies and which screen uses which; 04's loading notes)
- `_design/rendering.md` (`ui` toolkit, "Loading")
- `_design/plugins.md` and `_design/scripting.md` (As built (FW-14))

## What

No screen looks empty, broken or finished while its data is on the way. The trigger: the launcher's agent cards
were missing for about 5 s with nothing to say why, and No agent was picked meanwhile, so ⌘↵ started a session with
no agent.

- `plugin.Load[T]`: a fetch off the loop, tracked as Idle / Pending / Ready / Failed; stale results dropped,
  timeouts and panics fail it, a reload keeps the value.
- `widget.Async` draws a section by policy (Block, Gate, Optional, Inline), with `Busy`, `ProgressBar`, `Skeleton`
  and `ButtonOpts.Busy`; the same in Lua (`oriel.load`, `oriel.ui.async`…).
- Every screen that waits moves onto them; work that froze the loop moves off it.

## Done when

- No view in `app/` draws its own "Reading…" (`TestViewsUseTheLoadingToolkit`).
- The toolkit's states have tests and a gallery sheet (`ui/widget/testdata/loading-*.png`); the loader's stale,
  timeout and panic cases have tests.
- The launcher shows skeleton cards and the elapsed time while it probes, picks nothing, and holds Start; a failed
  probe shows Retry.
- Each screen's loading, failed and empty states are tested through the toolkit.
