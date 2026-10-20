---
id: SE-8
title: "Tags from the Repos page: branch, pull first, signed, message template, push only the tag"
phase: P5
status: done
area: Sessions and AI
module: host, app, gitx
depends_on: [SE-7]
---

# SE-8 · Tags from the Repos page

**Phase** 5 · AI (follow-up) · **After** [SE-7](SE-7-repos-page-stats-housekeeping.md) · **Unblocks** nothing yet

## Design

- `_design/mockups/45-repos.png` (panel 1, Tags), `_design/ui.md` 45

## What it is

- New tag: branch, the next semver bumped, message from `repos.tag.message`, pull first (`repos.tag.pull`), signed
  (`repos.tag.sign`), push only that tag (`repos.tag.push`). Always annotated. `repos.tag` does the same from Lua.

## Done when

- An annotated tag on a pulled branch, only it pushed; an existing tag and a pull over a dirty branch are refused; a
  failed push keeps the tag and offers Push again; created from the page.
