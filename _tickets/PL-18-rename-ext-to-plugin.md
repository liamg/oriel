---
id: PL-18
title: "Rename the ext package and Extension types to plugin"
phase: P8
status: done
area: Platform
module: ext
depends_on: [PL-3]
---

# PL-18 · Rename the ext package and Extension types to plugin

**Phase** 8 · plugin registry · **After** [PL-3](PL-3-all-or-nothing-install-consent-update-capability.md) · **Unblocks** nothing yet

## Design

- `_design/catalog.md` (naming)
- `_design/architecture.md` (module layout)
- `_design/plugins.md` (Go interfaces)
- `_design/features.md` (this row)

## What

- `ext` → `plugin`, `ext.Extension` → `plugin.Plugin`, `app.Config.Extensions` → `Plugins`, and the matching doc and UI strings.
- File-extension code (`syntax.Language.Extensions`) is a different meaning and keeps its name.

## Done when

- No `Extension` identifier or "extension" UI string is left outside file-extension code.
- `architecture.md` and `plugins.md` match.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
