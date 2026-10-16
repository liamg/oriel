---
id: PL-22
title: "Lazy loading: commands, events, keys, filetypes and omnibox sources load a plugin when first used"
phase: P8
status: todo
area: Platform
module: app, registry
depends_on: [PL-20]
---

# PL-22 · Lazy loading

**Phase** 8 · plugin registry · **After** [PL-20](PL-20-plugins-in-settings-toml-and-lockfile-consent.md) · **Unblocks** nothing yet

## Design

- `_design/plugins.md` (Plugin manager: "Lazy load on command, key, event, filetype or omnibox source")
- `_design/catalog.md` (`lazy` in the manifest and as a `[plugins]` override)

## What

- A plugin whose manifest (or its `[plugins]` entry) has `lazy` isn't loaded at start. It loads the first time one of
  its commands (globs: `Jira*`) is run, one of its events happens, one of its keys is pressed, a buffer of one of
  its filetypes opens, or its omnibox source's prefix is typed, and then the trigger goes on as if it had always been
  loaded.
- The general pieces go into the registry: a placeholder for a command, a key and an omnibox source that something
  will provide.
- `:Plugins` shows which are loaded and which wait.

## Done when

- A plugin with `lazy = { commands = ["Hi*"] }` isn't loaded until `:Hi` runs, and `:Hi` then works the first time.
- The same for an event, a key and a filetype.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
