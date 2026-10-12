---
id: FW-3
title: "Registries: commands, keymaps (modes, chords, panel-local), options (scoped)"
phase: P1
status: todo
area: Framework (first, after the spike)
module: registry, input
depends_on: [FW-1]
---

# FW-3 · Registries: commands, keymaps (modes, chords, panel-local), options (scoped)

**Phase** 1 · framework · **After** [FW-1](FW-1-module-skeleton-and-core-interfaces-wired-end-to.md) · **Unblocks** [ED-5](ED-5-plain-engine-and-the-input-engine-interface-emac.md), [PL-1](PL-1-lua-config-and-plugins-hooks-inspect-luals-types.md), [SH-4](SH-4-k-omnibox-sessions-commands-files-symbols-ticket.md), [SH-6](SH-6-which-key-popup.md)

## Design

- `_design/plugins.md`
- `_design/scripting.md`
- `_design/editor.md`
- `_design/architecture.md`
- `_design/features.md` (this row)

## Mockups

![19-config](../_design/mockups/19-config.png)

## Done when

`:Keys`/`:Commands` list sources; chords resolve with timeouts; scope precedence tests

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
