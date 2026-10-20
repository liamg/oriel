---
id: PL-19
title: "Package format: Lua entry point, process/wasm/asset APIs, oriel plugin pack"
phase: P8
status: done
area: Platform
module: plugin, script
depends_on: [PL-2, PL-3]
---

# PL-19 · Package format: Lua entry point, process/wasm/asset APIs, oriel plugin pack

**Phase** 8 · plugin registry · **After** [PL-2](PL-2-process-plugins-with-an-os-sandbox-wasm-plugins-.md), [PL-3](PL-3-all-or-nothing-install-consent-update-capability.md) · **Unblocks** [PL-20](PL-20-plugins-in-settings-toml-and-lockfile-consent.md), [PL-16](PL-16-the-oriel-plugins-registry-repo-and-ci.md)

## Design

- `_design/catalog.md` (What a plugin is, Manifest, Assets, Publishing → `oriel plugin pack`)
- `_design/plugins.md` (tiers)
- `_design/scripting.md` (API sketch)
- `_design/security.md` (where a plugin runs)
- `_design/features.md` (this row)

## What

- A plugin is a directory with `oriel.toml` and, unless it's data-only, `init.lua`. The manifest's `run` field goes.
- `oriel.process.spawn(path)` and `oriel.wasm.load(path)`, scoped to the plugin's own directory, with no `exec` grant.
  Anything outside it still needs `exec`.
- `oriel.asset(path)`, read-only, no capability needed.
- `[contributes]` registers themes, fonts, icons, languages, prompts and help with no Lua.
- The 20 MB size limit.
- `oriel plugin pack` and `oriel plugin init [--process]`.
- Spike: an ad-hoc-signed downloaded binary runs under sandbox-exec on macOS.

## Done when

- A data-only theme plugin, a Lua plugin, and a Lua + process plugin each load from a directory and register their contributions.
- `oriel.process.spawn` outside the plugin's directory fails with a missing `exec` error.
- `pack` rejects a plugin with an undeclared capability, and one over the size limit, and prints sha256s for a good one.
- The sandbox-exec spike result is recorded in `catalog.md`.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
