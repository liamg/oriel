# New project, retiring crit

## Recommendation: a new repo

- The new product shares concepts with crit but almost none of its UI code. A
  "v2" branch would carry 30k lines of browser JS we're deleting.
- A clean history and licence make open-sourcing simpler: crit's history holds
  the room, media scraping and personal config.
- Go packages port by **copying** the ones listed in `architecture.md`, not by
  importing crit.

## Order

1. Create `github.com/liamg/oriel` with `go.work` and the module skeleton.
2. Port the packages that don't need rethinking: `sandbox`, `egress`,
   `gateway`, `mcp`, `gh`, `git`, `agentbus`, `linear`, `slop`, `prompts`, `quota`.
3. Build phases 0–4 (see `PLAN.md`). crit stays your daily driver for AI work.
4. Phases 5–6: Oriel's session host keeps the workspace layout and manifest fields crit uses,
   but lists only its own sessions; crit's stay in crit.
5. Once phases 5–6 are done: crit stops getting features. Fixes only.
6. After phase 7 has had two weeks of daily use: archive crit, and its README points at Oriel.

## Data carried across

- Workspaces and worktrees: same layout, read in place.
- Threads, tasks and comment history: imported once, by a converter.
- Prompts overrides (`prompts.json`), settings, trusted orgs.

## Not carried

- Room config, scenes, media settings, puzzle history, the post-it notes.
