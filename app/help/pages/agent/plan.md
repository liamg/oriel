---
id: agent/plan
covers: [plan, plan.docs]
related: [agent/requests, agent/launcher, agent/console]
---

# Plan

A plan session drafts the change as markdown docs before any code is written: `PLAN.md`, and one more doc per big
piece, at the workspace root. You read them here as documents, comment on their lines, and press **Start work** when
the plan is right. It opens by itself when a plan session comes to the front; `plan.open` opens it again.

## Header

The doc's title, the agent and how many times it has revised the doc, and what it's editing now. **Preview diff**
shows the docs as a diff when something offers one. **Start work** is lit once the plan is ready (see Readiness).

## Doc

The doc rendered: headings, paragraphs, lists, tables, fenced code and mermaid diagrams (click one to zoom it). A
line number shows beside a line with a thread on it, and its edge takes the thread's colour. The agent's latest edit shows in green for a few minutes, what it took
out struck through. Click a line, or move with `j` and `k` and press `c` or `⏎`, to comment on it.

## Docs

Every doc at the workspace root, `PLAN.md` first, with how many of its threads need you (red), are being worked on
(blue) or are open. Clicking one shows it, bringing the plan to the front from behind whatever tab was there. Below
them: where the plan came from, and the repos it's about with their branch. A plan started from a ticket shows
**Ticket ENG-2231**, which opens the ticket in a tab of its own (`ticket.open`); otherwise, the prompt it started with.

## Threads

Threads sit in the margin beside the lines they're about. A comment goes to the agent at once, anchored to its line
(it follows the line as the doc is edited). Reply in the card; **resolve** closes the thread, and **defer** puts it
off without holding up Start work. A closed thread folds to one line: click it to read the thread while it stays closed (its chevron collapses it again), or
**reopen** it.

## Readiness

How many threads still hold up Start work. Start work unlocks when every thread is resolved or deferred; the
plan's own thread, where the prompt was, and side questions don't count.

## Start work

Opens a dialog of how the plan becomes work; `⌘↵` starts, `esc` or Cancel closes it.

- **Clear the plan's threads**: resolved threads go, and open or deferred ones go into what the agent is told, so
  nothing asked is lost. Off, they stay.
- **Restart the agent with a fresh context**: a new agent starts from the plan docs and the work prompt. Off, the
  plan's agent carries on with what it knows.
- **Branches**: each repo's branch, named from the ticket and prompt; change it here.
- **Sandbox**, **Open network**, and the **model** and **effort** for the work.

Start work makes a worktree of each repo on its branch, tells the agent the plan is approved and where to implement
it, and moves the window to the worktree. From then on it's a work session: comments arrive on the code.
`session.start-work` does the same from Lua, with `clear`, `fresh`, `branches`, `sandbox`, `network`, `model` and
`effort`.
