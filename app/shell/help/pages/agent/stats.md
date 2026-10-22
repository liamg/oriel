---
id: agent/stats
covers: [stats.open, stats.session, stats.origin]
related: [agent/usage, agent/console]
---

# Stats

What the agents cost and did, from their own transcripts. Every cost is **est.**: tokens are counted, and the cost is
tokens × each model's list prices (input, output, cache reads and writes, fast mode and long prompts where they cost
more), so a price change re-costs history. The prices are [cost.dev](https://cost.dev)'s: Oriel ships a copy, fetches
the list again when it starts and once a day, and keeps the latest in `~/.oriel/prices.json`; offline, it uses the
last one it had. Resting on **COST · EST.** says when cost.dev last updated them. A model the list doesn't price
(one retired before it) costs nothing here rather than a guess.

## Global stats {#page}

`:Stats` or `⌘⇧S`, or **Stats** in the sidebar: every session, grouped by repo.

## Oriel and outside it {#origin}

Oriel also reads every transcript Claude Code and Codex keep on this machine (`~/.claude/projects`, or
`$CLAUDE_CONFIG_DIR`'s, and `~/.codex/sessions`, or `$CODEX_HOME`'s), so what you run in a terminal or another tool
counts too. A transcript an Oriel session owns is that session's; any other is a session **outside Oriel**, named by
its first prompt and its repo taken from where it ran. Nothing is counted twice.

**ALL**, **ORIEL** and **OUTSIDE** in the toolbar (`:Stats origin=outside`) keep the page to one side; grouping by
origin (`g` `o`) stacks the two day by day. The Tokens tile says how much of the range was outside Oriel, and in the
sessions table an outside one is marked **outside**.

## Range {#range}

7, 30 or 90 days, or a custom range. `[` and `]` step to the shorter or longer one. The tiles compare the range with
the period of the same length before it.

**CUSTOM** (or `c`) opens a calendar on the range shown: the arrows move the day (a week with ↑ ↓, a month with
PageUp/PageDown), `⏎` picks one end and then the other, and `Esc` closes it. A click picks a day the same way, and a
range can be typed as `2026-09-01..2026-09-30`, as can `:Stats 2026-09-01..2026-09-30`. Once it's custom, the ▾
beside it opens the calendar again.

Everything counts only what happened in the range: a session that runs over its edges brings just its part, tool
calls included. **Active** is the time spent working, "of" the sessions' wall-clock: on each day a session worked,
its first record to its last, added up.

## Group by {#group-by}

Repo (the default), session, kind, model, provider, account, origin or day: `g` then `r`, `s`, `k`, `m`, `p`, `a`, `o`
or `d`.
Cost adds up across accounts, so every account is counted unless a chip says otherwise. A session counts for the
account it ran on at the time: signing in to another later moves only what comes after.

## Filters {#filters}

Chips narrow what's counted: a repo, a provider, an account, and with **+ filter** a session, kind or model. A chip's ✕
takes it off.

## Splitting multi-repo sessions {#split}

A session over several repos charges each request to the repo it worked in: that of its own last file tool call
(Read, Edit, Write, Grep, Glob), else of the latest one before it. Requests before any file work, or working outside
every repo, split by each repo's share of the file tool calls; a session with no file work splits evenly. Off, each repo
is charged the whole session: the rows overlap, and the total stays the real one. One setting,
`stats.split_sessions`, drives this and the session tab's **count whole session in each**.

## Chart {#chart}

Cost per group per day, stacked, in the repos' project colours. Hovering a row or a bar picks the series out.

## Table {#table}

The groups with their share and the last 30 days. `j`/`k` move, `⏎` or a click drills in (a filter chip, regrouped),
`⌫` backs out. **Copy as CSV** copies the table.

## Session stats {#session}

`:Stats session`, the cost in the session stats box, or **Session stats** on the page filtered to one session (a
session outside Oriel too): one session as a main tab. Its cost over its life by token
kind with idle gaps, turns and compactions marked, its cost per repo, models, tools with errors and timings, hours
and effort.
