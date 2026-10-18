---
id: PL-6
title: "Self-update for the app and workers (self-signed, ed25519)"
phase: P7
status: done
area: Platform
module: update
depends_on: [AI-1]
---

# PL-6 · Self-update for the app and workers (self-signed, ed25519)

**Phase** 7 · the rest · **After** [AI-1](AI-1-worker-wire-sessions-workspaces-with-multi-repo-.md) · **Unblocks** [PL-21](PL-21-registry-client-badges-and-the-plugins-page.md)

**Note:** workers are gone (one Oriel per machine, see [AI-15](AI-15-workers-page-remote-workers-pairing.md)): the updater installs only this copy; nothing is pushed to workers.

## Design

- `_design/distribution.md`
- `_design/architecture.md`
- `_design/features.md` (this row)

## Done when

an update round-trips on macOS and Linux with rollback on a failed health check

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
