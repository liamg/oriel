---
id: PL-3
title: "All-or-nothing install consent, update capability diff"
phase: P7
status: todo
area: Platform
module: ext
depends_on: [PL-2, ED-11]
---

# PL-3 · All-or-nothing install consent, update capability diff

**Phase** 7 · the rest · **After** [PL-2](PL-2-process-plugins-with-an-os-sandbox-wasm-plugins-.md), [ED-11](ED-11-languages-as-plugins-core-grammar-set-and-local-packs.md) · **Unblocks** nothing yet

## Design

- `_design/security.md`
- `_design/plugins.md`
- `_design/features.md` (this row)

## Mockups

![29-plugin-install](../_design/mockups/29-plugin-install.png)

## Grammar packs

- Languages beyond ED-11's core set install as grammar packs through the plugin manager. The packs are built by
  our CI from the pinned grammar manifest and published with checksums; the binary ships the checksum list and
  installs only packs that match it, as `deps.md` requires (no prebuilt `.wasm` from anywhere else). An installed
  pack lands in the languages directory ED-11 loads from.

## Done when

Acceptance tests derived from the design docs above, written first, all passing in CI on macOS and Linux.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
