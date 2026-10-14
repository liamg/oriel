---
id: AI-14
title: "Stats and Usage follow-ups: custom ranges, exact per-range tools, plan tiers, active span, accounts at the time"
phase: P5
status: review
area: Sessions and AI
module: worker, app, ui
depends_on: [AI-12]
---

# AI-14 · Stats and Usage follow-ups

**Phase** 5 · AI (follow-up) · **After** [AI-12](AI-12-stats-and-usage-pages-grouped-by-repo.md) · **Unblocks** nothing yet

The gaps AI-12 left against mockup 43, filled.

## Design

- `_design/mockups/43-stats.png`, `_design/ui.md` 43, `_design/agents.md` (stats, accounts)

## What it is

- **Custom range:** CUSTOM opens a general `widget.DatePicker` (a range of days, with the keyboard: arrows move, ⏎
  picks, Esc cancels; typed `2026-09-01..2026-09-30`), usable from any plugin and Lua.
- **Exact per-range tools:** the worker's stats keep per-request tool calls by day (counts, errors, timing buckets),
  so the Stats page's tools and their error rates count only calls inside the range, not whole sessions.
- **Plan tier chip:** each account's plan ("MAX 20×", "Team", "Pro") from the provider's own account data, on the
  Usage page's account header and its switcher; unknown shows nothing rather than a guess.
- **Active span:** the Active tile shows "of 182h", the wall-clock span of the range's sessions, as 43 does.
- **Account at the time:** a session records the account it ran on when it starts (and on a login change), so a
  later login doesn't move its history to the new account.
- **Verified sources:** the Claude account fields and the gpt-5 prices AI-12 wrote from memory are checked against
  the real `~/.claude.json` shape and OpenAI's pricing, with fixtures from them.

- **Outside Oriel:** the stats also read the transcripts of sessions run outside Oriel (a terminal, another tool),
  marked as such, so Oriel's tokens and the rest can be told apart (see AI-12, "Everything they can find").

## Done when

- Picking a custom range with the date picker (mouse and keys) re-queries; tools on the Stats page match the calls in
  that range in fixtures spanning its edges.
- The Usage header shows the plan tier from a fixture account file; an account whose plan isn't known shows none.
- The Active tile reads "of Nh" from fixtures.
- A session started before a login change stays with its first account in Stats and Usage.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
