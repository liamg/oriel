---
id: SE-5
title: "Jobs and login follow-ups: multi-line prompts, pages that replace each other, run tabs, schedules, remote log in"
phase: P5
status: done
area: Sessions and AI
module: app, worker, ui, agent
depends_on: [SE-4, AI-13]
---

# SE-5 · Jobs and login follow-ups

**Phase** 5 · AI (follow-up) · **After** [SE-4](SE-4-jobs-page-saved-prompts-runs-and-schedules.md), [AI-13](AI-13-detect-an-expired-agent-login.md) · **Unblocks** nothing yet

**Note:** workers are gone (one Oriel per machine, see [AI-15](AI-15-workers-page-remote-workers-pairing.md)): a stop on quit is a hand-over, missed occurrences run when Oriel starts, a job has no worker to change, and there's no remote login.

The gaps SE-4 and AI-13 left, filled.

## Design

- `_design/mockups/44-jobs.png`, `_design/mockups/04-launch.png`, `_design/ui.md` 44 and 04, `_design/agents.md`
  ("A login that stops working")

## What it is

- **A multi-line input** (`widget.Input` gains `Lines`: wraps, grows to a max height then scrolls, `⏎` a new line,
  `⌘⏎` submits), used by the job form's prompt and the launcher's prompt, and in Lua.
- **Full-window pages replace each other:** opening Jobs, Stats, Usage or Settings closes whichever is up, through
  `widget.Page` (one page at a time, a general rule), not per page.
- **A run in its session tab:** Open on a run brings up its session as 44's panel 3 draws it (the main tab, the
  narrow sidebar, the job card beside the console); run tabs read "job: <title>".
- **Who stopped a run:** a stop records who (you, the agent, the worker on shutdown), so the row says "Stopped by you".
- **Rows open from Open:** clicking a run row selects it; Open/Resume open it.
- **Schedules:** a skipped run is kept in the run list as "skipped (last run still going)"; occurrences missed while
  the worker was off are run once when it starts (one per job, not a backlog); a job's worker can be changed.
- **Log in on a remote worker:** Log in opens the agent's own terminal from that worker (`term.open {source}` over the
  wire, as Raw terminal already does) with `/login` typed, the same as a local one.
- **A real login check:** Check again asks the provider whether the login works, not only its files (Claude: a
  zero-token request the CLI already makes at start, if one exists; else its own status command), so a revoked token
  on disk reads as signed out.
- **Codex's error recorded:** the unauthorized turn error and `account/updated` from a recorded Codex session.

## Done when

- The job form's and launcher's prompts take several lines; `⌘⏎` submits.
- ⌘⇧J over Stats leaves only Jobs up; the same for each pair of pages.
- Open on a run shows panel 3's layout; a stopped run says who stopped it; a click on a row selects it.
- A run skipped while one is going shows in the list; a missed occurrence runs once after a worker restart on a fake
  clock; a job's worker can be changed.
- Log in on a fake remote worker opens its terminal with `/login`; Check again with a revoked token on disk stays
  signed out against a fake provider.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
