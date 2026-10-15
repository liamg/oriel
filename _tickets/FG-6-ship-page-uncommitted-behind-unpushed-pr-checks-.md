---
id: FG-6
title: "Ship page: uncommitted → behind → unpushed → PR → checks, multi-project"
phase: P6
status: review
area: Files, git, terminals
module: gitx, gh
depends_on: [FG-2]
---

# FG-6 · Ship page: uncommitted → behind → unpushed → PR → checks, multi-project

**Phase** 6 · review · **After** [FG-2](FG-2-gutter-hunks-stage-and-unstage-hunks-inline-blam.md) · **Unblocks** [AI-10](AI-10-commit-and-pr-drafts-when-the-agent-goes-idle.md), [RV-4](RV-4-github-review-mirroring-and-forward-to-github.md)

## Design

- `_design/ide.md`
- `_design/ui.md`
- `_design/features.md` (this row)

## Mockups

![18-git](../_design/mockups/18-git.png)

## Done when

flows tested against fixture repos and a fake GitHub

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.

## From SH-16

- The rail already has an entry for this view, greyed until the view is registered; registering it lights the entry up.

## From SH-17

- The project sidebar has each project's **Pull request** entry and the session-wide **Ship** entry, greyed until
  this registers them. Pull request in the dock is the compact checklist (committed → up to date → pushed → PR →
  checks); **Raise PR** / **Edit PR** opens a full-width main tab (title and body, reviewers, cross-linked PRs, the
  "before this ships" gates); Ship is 18's full page. See `ui.md` 01h and `42-project-sidebar.png` panel 6.
