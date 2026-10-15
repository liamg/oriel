---
id: PL-17
title: "oriel plugin publish"
phase: P8
status: review
area: Platform
module: cmd
depends_on: [PL-16, PL-21]
---

# PL-17 · oriel plugin publish

**Phase** 8 · plugin registry · **After** [PL-16](PL-16-the-oriel-plugins-registry-repo-and-ci.md), [PL-21](PL-21-registry-client-badges-and-the-plugins-page.md) · **Unblocks** nothing yet

## Design

- `_design/catalog.md` (Publishing)
- `_design/features.md` (this row)

## What

- Run in a plugin repo: read `oriel.toml` and the current tag, then add a `[[versions]]` entry, or create the
  `plugin.toml` for a new plugin.
- Fork, branch and open the PR with `gh`.
- On a trusted plugin, check the tag and report that there's nothing to publish.

## Done when

- Against a fake GitHub, publishing a new version and a new plugin each open a PR with the right `plugin.toml` diff.
- On a trusted plugin, publish opens no PR.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
