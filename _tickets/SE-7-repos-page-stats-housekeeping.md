---
id: SE-7
title: "The Repos page: list, detail, stats, housekeeping of merged and gone branches with their worktrees"
phase: P5
status: review
area: Sessions and AI
module: host, app, gitx
depends_on: [SE-6, FG-7]
---

# SE-7 · The Repos page

**Phase** 5 · AI (follow-up) · **After** [SE-6](SE-6-repos-list-discovery-add-repo-dialog-launcher-checklist.md), [FG-7](FG-7-project-overview-stats-for-each-repo-with-nothing-open.md) · **Unblocks** [SE-8](SE-8-tags-from-the-repos-page.md)

Repos get a home: a page to see, add, remove and look after them.

## Design

- `_design/mockups/45-repos.png` (panel 1), `_design/ui.md` 45, `_design/sessions.md` → Housekeeping

## What it is

- `widget.Page` over the main area, opened by `repos.open`, `:Repos`, the sidebar, the start view and the omnibox.
- The list; the detail: Fetch, Reveal, Remove, New session; stats (`host.RepoFacts` and the overview's panels).
- Housekeeping (`host.Housekeeping`, `host.Housekeep`, `repos.housekeep`): merged or upstream-gone branches with their
  worktrees, stale worktrees; never the default, current or a live session's branch; dirty worktrees only when ticked.

## Done when

- Merged and upstream-gone branches are offered, never the default, current or a live session's; a dirty worktree
  goes only when forced; Clean up from the page deletes them.
- The page matches 45.
