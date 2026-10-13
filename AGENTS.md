# Oriel: instructions for agents

Oriel is a native, vim-first, AI-first IDE written in Go: half professional editor, half agent harness.
It replaces crit. Everything decided so far is in `_design/`, and the work is tracked in `_tickets/`.

## Where things are

- `_design/PLAN.md`: goals and every decision made. Read it first.
- `_design/features.md`: every feature with its mockup, module, phase and **"done when"**.
- `_design/*.md`: one doc per area (see `_design/README.md`). `_design/mockups/*.png` are the screens to match.
- `_design/crit-reference.md`: what crit does today, with source paths in `github.com/liamg/crit`, for anything we port.
- `_tickets/`: one markdown ticket per feature. `_tickets/README.md` indexes them by phase.

## Working a ticket

When asked to do a ticket ("do PL-8", "do the Linear plugin"):

1. Find it in `_tickets/`. Read it, then **every design doc and mockup it links**.
2. Check its phase is unblocked: earlier phases' exit criteria pass. If not, say so before starting.
   Then look for what already exists that the ticket can build on (widgets, registry and host helpers, `gitx`, test
   harnesses), and name the general pieces it needs that don't exist yet: they're built first, as their own units.
3. Write the acceptance tests first, from the ticket's "done when" and the design docs. They fail.
4. Implement until they pass on macOS and Linux.
5. If the code has to differ from the design, update the design doc in the same change and say why.
6. Set the ticket's `status` (todo → doing → review → done).

If the design is silent or ambiguous on something that matters, ask rather than invent.

## Rules

- **Extensible by construction, never hacked in.** Every feature is built on the same public APIs a plugin would use
  (registry, events, hooks, views, decorations, commands, providers; see `_design/plugins.md`). Concretely:
  - No special-case methods, flags or back doors for one feature. If a feature needs something the API can't
    express, **extend the API generally** (and its Lua/RPC bindings), then build the feature on it.
  - Built-ins are written as if they were plugins: replaceable, removable, and configurable from Lua.
  - A good test: could someone build this feature as a third-party plugin with the same result? If not, the API is incomplete. Fix that first.
  - New contribution points or API surface go into `_design/plugins.md` / `scripting.md` in the same change.
- **Reuse before you build, everywhere.** Search for an existing component, helper or harness before writing one.
  When the same thing would exist twice, extract it into the package that owns it and use it from both. Fix a
  behaviour where every user gets it (the toolkit, the shared package), never by patching one screen or caller.
  - **UI**: views are composed from `ui/widget` (`Clickable`, `Button`, `IconButton`, `Segmented`, `Tabs`, `List`,
    `Chip`…). No view repeats `c.Next` / `c.Interaction(...).Clicked` / `Interactive` / `Cursor` / `Role` to make
    something clickable (`TestViewsUseWidgetsForClicks` fails if one does). If no widget fits, add a general one to
    `ui/widget` with its theme tokens and a test, then use it.
  - **UI behaviour lives in the toolkit too** (`_design/rendering.md`), so every screen gets it and none re-solves it:
    - A float's `Z` is a named layer (`ui.LayerPage`, `ui.LayerDialog`…), never a number; a float inside another
      inherits its layer (`TestFloatsUseLayers`).
    - A full-window page is `widget.Page`; a dialog over a scrim is `widget.Scrim` (an outside click closes it)
      with Escape. Any closable surface gets both an Escape path and a visible close (a tab's ✕, a button). Docked
      views close with Escape and their tab's ✕.
    - A key that never arrives may be the backend's: gio keeps Tab for its own focus moves unless the window's key
      filter names it (`render/gio/window.go`).
    - A widget hands keys it doesn't use up with `Ctx.PassKeys`, so Escape on a focused control reaches its page.
    - `Ctx.Focus` from under the float that holds focus is ignored; a page that grabs focus every frame can't steal
      it from a dialog over it. Test a new overlay opened over the others (settings, the omnibox), not alone.
    - A frame that reads input asks for the next; don't add a `WakeAt(now)` to show what a click changed.
    - Text fields edit with the platform's keys (`widget.Input`); a number field takes `Accept: widget.Digits`
      and shows `Invalid` while it's wrong.
  - **Code**: shared logic lives in the module that owns it (`gitx` for git, `registry` for contributions, `core`
    for state), not copied into each caller; tests share harnesses (`errScene`, `startApp`, `vimtest`) rather than
    growing new ones.
- **Test-first, with sources.** Behaviour tests cite a vendored spec with a verbatim quote, and vim expectations are
  checked against headless nvim (`_design/testing.md`). Never write an expected value from memory, and never weaken
  a failing test to get to green.
- **Test the way it's used.** Drive a control through the toolkit (click it, press keys on it, with timeouts on and
  over other overlays), not by setting the state it would set; a test that turns off a timeout or opens one view
  alone misses the bugs people hit.
- **Names are plain words.** Public API (Lua, commands, options) says what it is: `oriel.option`, not `oriel.o`; a
  user who wants it short aliases it (`local o = oriel.option`).
- **Dependencies.** Stdlib, `golang.org/x/*` and `github.com/liamg/*` are free to use. Anything else must be listed and
  approved in `_design/deps.md` first. Ask; don't add.
- **Claude is driven through its interactive CLI in a PTY with hooks, never `claude -p`** (that path is billed per token)
  (`_design/agents.md`).
- **Platforms.** macOS (primary) and Linux, from the start. No platform-specific code without the other platform's equivalent or a build tag.
- **Interfaces at seams.** Follow the module layout and interfaces in `_design/architecture.md`. `text`, `vim`, `core`
  and `agent` never import `ui` or `render`.
- **Commits**: one commit per ticket, conventional style with the ticket id (`feat(vim): ED-2 motions and operators`).
  **Never push**: the user pushes.

## Coding style

- No essay-style comments. Keep comments short and to the point; no 10-line comments before 3-line functions.
- No comments about history or fixed bugs; that's what git is for. Comments describe the code as it is now.
