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

## As the work goes {#moves}

A session started from a ticket moves it into progress, then into review once its PR is raised, and links the PR
back. Once the session's PRs are all merged, Oriel asks whether to mark the ticket done (an Inbox card): a ticket may
need more PRs, or something done by hand, so it's never marked done without a yes. **Not yet** leaves it as it is.

## In a session {#agent}

A tracker's plugin can give every session's agent tools for its tickets. Linear's are `linear_issue` (read a ticket
with its comments and attachments), `linear_update_issue` (its title, description, state, priority, assignee or
project, by name), `linear_comment`, `linear_create_issue` (in a team, and a project if you name one) and
`linear_projects` (the projects to file a ticket in or move one to). They act as the owner of the API key, and don't
ask first: ask the agent to move a ticket, note what it found, or file a follow-up in a project, and it does.
