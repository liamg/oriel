---
id: shell/mission
covers: [app.overlay.mission, mission.*]
related: [shell/inbox, shell/tabs, shell/hud, agent/asks, agent/console]
---

# Mission control

Every session at once, as a card, with what each one is doing or waiting on. `⌘⇧K` shows it from anywhere and puts it
away again; `Esc` goes back to where you were.

## The page {#page}

Along the top: how many sessions need you, want permission, are working, running CI, with a reviewer or quiet; how
the cards are grouped; and a filter. On the left, the queue of what needs you. Along the foot, the strip of
indicators and the keys.

## Cards {#cards}

One card for each session that isn't archived. Its border is its status colour, and the card you've selected is
outlined in the accent. A click selects a card; its kind, its pull request and its agent are in its heading, and its
tasks, context, change and how long it's been at its foot.

## In the background {#background}

When the agent's last turn left work running that will wake it again (a long test run, a background subagent), the
card's foot says **+2 in background**; hover it for what each is. The session counts as working until that's done.

## Opening a session {#open}

`⏎`, or a click on a card's name, brings its session to the front, resuming its agent if it was stopped, and puts
mission control away.

## Live output {#live}

A working card shows its agent's activity as it happens, in a small terminal: each tool call with a dot (cyan while
it runs, red when it fails) and the end of what it printed, in its own colours, and the agent's prose as it writes it.
`agent.terminal` shows the agent's own raw terminal.

## Asks on a card {#asks}

A card whose session waits on you shows what it waits on: a permission, a held connection or gateway call, a
question. It's the same card the inbox shows, answered here the same way: Allow once, Always, Deny, or an option.
`a` allows what the selected card asks.

## Grouping {#group}

**By status** (most urgent first) or **by repo** (each session's primary repo). `mission.group` with `by` does the
same.

## Filter {#filter}

`/` types a filter: only the sessions whose name, kind, status, branch, repos, ticket or PR match it are
shown. `⏎` or `Esc` leaves the field.

## Needs you {#queue}

Everything waiting on you, oldest first: each card the inbox holds, and each session that needs you with nothing to
answer (it replied, and waits for you). A click selects its session's card; a card with no session opens under its
row. Other people's agents' questions are apart, with how long until they're answered for you.

## New session {#new}

`n` opens the launcher to start a session; with no sessions yet, so does **New session**.

## The strip {#strip}

Indicators in the `mission` slot: your plan's usage for each provider, and this machine's CPU over the last 15
minutes. A plugin adds its own with an indicator in that slot.

## Keys {#keys}

`h` `j` `k` `l` (or the arrows) move between cards, `⏎` opens, `a` allows, `x` stops the agent, `r` renames the
session on its card, `n` starts a new one, `/` filters.

{{keys "mission.*"}}

## At launch {#launch}

Oriel opens on mission control when it starts with nothing open, as soon as there's a session to show.
`mission.at_launch` turns that off.

{{options "mission"}}
