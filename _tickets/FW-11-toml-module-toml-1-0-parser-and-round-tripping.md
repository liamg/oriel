---
id: FW-11
title: "toml module: TOML 1.0 parser and round-tripping editor"
phase: P1
status: done
area: Framework (first, after the spike)
module: toml
depends_on: [P0-1]
---

# FW-11 · toml module: TOML 1.0 parser and round-tripping editor

**Phase** 1 · framework · **After** [P0-1](P0-1-repo-skeleton-go-work-module-stubs-from-architec.md) · **Unblocks** [FW-7](FW-7-theme-tokens-layered-resolution-live-reload-the-.md), [SH-12](SH-12-settings-page-settings-sections-search-typed-co.md)

## Design

- `_design/settings.md` (The TOML module)
- `_design/theming.md`
- `_design/features.md` (this row)

## Done when

the `toml-test` suite (vendored under `spec/`) passes; set, add and delete a key leave every other byte unchanged

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
