---
id: agent/stats
covers: [stats.open, stats.session]
related: [agent/usage, agent/console]
---

# Stats

What the agents cost and did, from their own transcripts. Every cost is **est.**: tokens are counted, and the cost is
tokens × the price table, so a price change re-costs history.

## Global stats {#page}

`:Stats` or `⌘⇧S`, or **Stats** in the sidebar: every session on every worker, grouped by repo. An offline worker's
last answer is still counted, marked stale in the header.

## Range {#range}

7, 30 or 90 days, or a custom range. `[` and `]` step to the shorter or longer one. The tiles compare the range with
the period of the same length before it.

## Group by {#group-by}

Repo (the default), session, kind, model, provider, account or day: `g` then `r`, `s`, `k`, `m`, `p`, `a` or `d`.
Cost adds up across accounts, so every account is counted unless a chip says otherwise.

## Filters {#filters}

Chips narrow what's counted: a repo, a worker, a provider, an account, and with **+ filter** a session, kind or model. A chip's ✕
takes it off.

## Splitting multi-repo sessions {#split}

A session over several repos is charged to each by its share of edits and file tool calls (Read, Edit, Write, Grep,
Glob); requests touching no file follow the same split, and a session with no file work splits evenly. Off, each repo
is charged the whole session: the rows overlap, and the total stays the real one. One setting,
`stats.split_sessions`, drives this and the session tab's **count whole session in each**.

## Chart {#chart}

Cost per group per day, stacked, in the repos' project colours. Hovering a row or a bar picks the series out.

## Table {#table}

The groups with their share and the last 30 days. `j`/`k` move, `⏎` or a click drills in (a filter chip, regrouped),
`⌫` backs out. **Copy as CSV** copies the table.

## Session stats {#session}

`:Stats session`, or the cost in the session stats box: one session as a main tab. Its cost over its life by token
kind with idle gaps, turns and compactions marked, its cost per repo, models, tools with errors and timings, hours
and effort.
