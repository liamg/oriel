---
id: PL-16
title: "The liamg/oriel-plugins registry repo and its CI"
phase: P8
status: todo
area: Platform
module: tools (repo)
depends_on: [PL-13]
---

# PL-16 · The liamg/oriel-plugins registry repo and its CI

**Phase** 8 · plugin registry · **After** [PL-13](PL-13-package-format-lua-entry-point-assets-plugin-pack.md) · **Unblocks** [PL-17](PL-17-oriel-plugin-publish.md)

## Design

- `_design/catalog.md` (The registry repo, Publishing, What the registry's CI checks, The index)
- `_design/deps.md` (allowlist rules)
- `_design/distribution.md` (signing key handling)
- `_design/features.md` (this row)

## What

- Create `liamg/oriel-plugins` (private for now) with `plugins/<author>/<plugin>/plugin.toml`.
- PR CI checks the owner, the tag and commit, `oriel plugin pack`, archives for every declared platform
  (Go rebuilt with `-trimpath` and compared), and dependencies against the allowlist.
- The CI comment leads with the capability diff and the compare link, and calls out `gate`.
- On merge: build `index.json`, sign it into `index.sig`, and publish both as a release. The key lives in CI secrets.
- Seed the repo with the first-party plugins as trusted.

## Done when

- A fixture PR for a new version gets the comment with the right capability diff.
- A PR for `acme/*` from someone outside acme fails the owner check.
- After a merge, the client from PL-15 verifies the published index.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
