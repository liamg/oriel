---
id: agent/tickets
covers: [Tickets, tickets.open, tickets.refresh, tickets.plan, tickets.work, tickets.browse, tickets.filter, app.overlay.tickets]
related: [agent/launcher, plugins/secrets]
---

# Tickets

The tickets assigned to you in your ticket trackers (Linear, or any tracker a plugin adds), and where you start
work on one. `:Tickets` lists every tracker's; `:Tickets linear`, or a tracker's own sidebar or start view entry,
lists one tracker's.

## The page {#page}

The header counts the tickets and says when they were last asked for. A tracker's list is kept for ten minutes;
**Refresh** (`r`) asks again now. `esc` or `✕` closes the page.

## The list {#list}

Most pressing first: priority, blocked ones last, then the ones under way, the ones with a PR, and by key. Each row
shows the ticket's key, title and state, its priority, project and PR, whether it's blocked, and "Continue ·
session" when a session already works on it. `j`/`k` move, `/` filters by key, title or project. A tracker that
still needs its API key, or that failed, says so at the top, with **Set key** or **Retry**.

## The ticket {#ticket}

The selected ticket shows what the list knows at once, then asks its tracker for the rest: the start of its
description, how many comments and attachments it has, and what blocks it.

- **Plan** (`↵`) opens the new session launcher on a plan, with the ticket's card. Starting it gives the agent the
  ticket with its comments and attachments. A ticket with a session shows **Continue** instead, which brings that
  session back.
- **Work** (`w`) opens the launcher on a work session, for when no plan is needed.
- **Open** (`o`) opens the ticket in its tracker, in the browser.
