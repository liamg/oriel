---
id: SE-6
title: "Repos as a list: ReposChanged, sibling discovery, remove and ignore, the Add repo dialog, the launcher's checklist"
phase: P5
status: done
area: Sessions and AI
module: core, host, app, ui
depends_on: [SE-1]
---

# SE-6 · Repos as a list

**Phase** 5 · AI (follow-up) · **After** [SE-1](SE-1-session-kinds-plan-work-review-tour-job-new-repo.md) · **Unblocks** [SE-7](SE-7-repos-page-stats-housekeeping.md)

Starting a session means picking repos from a list, not typing a path; adding one is its own flow, from anywhere.

## Design

- `_design/mockups/45-repos.png` (panels 2 and 3), `_design/ui.md` 04 and 45, `_design/sessions.md` → Repos

## What it is

- `core.Repo` gains `Source`, `Added`, `Used`; `core.ReposChanged` puts the list in the store.
- `repos.json` becomes `{repos, ignored}` and still reads the old array.
- Discovery: the main-clone siblings of known repos, one level, capped, never ignored ones; `repos.discover`.
- `repos.remove` unregisters and ignores; refused while a live session has a worktree of it.
- The Add repo dialog (`repos.new`): an existing directory with a live check, or a clone into a parent folder and
  name shown before it's made, with git's progress and `esc` to stop.
- The launcher's repositories are a checklist; **+ Add repo** comes back ticked. The sidebar's `+` ends with "Add a
  new repo…".

## Done when

- repos.json reads the old array; adds fire `ReposChanged` with what they added.
- A scan registers main-clone siblings one level deep, never an ignored one, never a worktree.
- The dialog adds a directory, and clones into the folder it shows, opened over the launcher, the repo coming back
  ticked.
