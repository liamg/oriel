---
id: agent/requests
covers: [requests, request, requests.summary]
related: [agent/console, agent/asks, editor/pane]
---

# Requests

A request is a thread with the agent: a comment on a line, or about the whole change, and the replies to it. The
Requests view lists them (`⌘⇧R`, or the rail); one opens in the main area to read and answer. A thread between the
editor's lines and its request here are the same thread: reply in either.

## List

Every request of the session in front, grouped by what it waits on: **Working** (the agent has it), **Needs you** (the
agent answered), **Open** (queued behind the turn, or a btw), **Resolved**. `j` and `k` move, `⏎` writes to the selected
one, `o` opens its anchor in the editor and `R` resolves it.

## Filters

All, or one group, with how many each holds.

## Search

Narrows the list to requests whose title or comments hold the words.

## New request

Starts a request that isn't anchored to a line (`⌘N` in the list). It goes to the agent when you send it.

## Request

The anchored code at the top, where it is now (it follows edits), then the conversation: your prompt as markdown with
what you pasted, and the agent's replies. **Open in editor** goes to the anchor; **Resolve** closes the request and
completes the tasks it asked for. At the right: where it is and who has it, the files it changed, the tasks and
requests it spawned, and other requests about the same file.

## Activity

What the agent did on the way to a reply: each edit (with its lines added and removed, and **view** to open the file),
lookup and test run.

## Evidence

A reply that says the tests pass links to the run behind it: ✓ or ✕, its counts and its id. A click opens the run:
the command, who ran it, what failed and what it printed. A claim with no passing run behind it is flagged ⚠.

## Composer

Write to the request: `⏎` sends, and the agent gets it at once when it's idle, or when its turn ends. **@ FILE** and
**# SYMBOL** open a picker, where `⌃a` attaches what you pick; a terminal's output and a failing test attach the same
way. `⌘⏎` sends now, interrupting the turn.

## Urgent

Sends the message in front of the running turn: the agent is interrupted and gets it at once.

## Btw

A side question, answered by a fork of the agent that has no tools and doesn't join the conversation.

## Between the lines {#inline}

**Ask about this…** in the editor's right-click menu (or `thread.new`) opens a thread on the cursor's line: `⏎` sends
it, `Esc` drops it. Open threads show under their line with the agent's state, and take replies there.

## Summary

The session at a glance, at the window's right (`requests.summary`): its requests, the tasks rail (✓ done, ◐ in
progress, ○ to do) and the stats: context, cache, cost, tasks and the latest tests.

{{keys "request*"}}
