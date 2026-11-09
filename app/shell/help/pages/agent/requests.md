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

Its tab carries its name; the header shows only its state. Right-click the tab for **Rename…**, which edits the name
in the header. A session started from a request is named by a small model
(Claude's Haiku), and its first request shares the name; `sessions.auto_name` turns that off. The anchored code at the top, where it is now (it follows edits), then the conversation: your prompt as markdown with
what you pasted, and the agent's replies. **Open in editor** goes to the anchor; **Resolve** closes the request and
completes the tasks it asked for. At the right: where it is and who has it, the files it changed, the tasks and
requests it spawned, and other requests about the same file.

## Comments {#comments}

A comment's **⋯**, at its top right while the pointer is over it: **Copy** takes its text as it reads, **Copy as
markdown** its source, and **View markdown** shows the source in a dialog. While a message of yours waits for the agent
(queued), the menu also has **Edit**, which puts it back in the composer (sending replaces it, the messages after it
staying where they are), and **Delete**, which takes out that message alone. `↑` in an empty composer edits your last
queued message.

## Evidence

A reply that says the tests pass with no passing run behind it is flagged ⚠. The runs themselves are in the agent
console, where a click opens one: the command, who ran it, what failed and what it printed.

## Composer

Write to the request: `⏎` sends, `⇧⏎` starts a new line, and the agent gets it at once when it's idle, or when its turn ends. `⌃a`
in a picker attaches what you pick; a terminal's output and a failing test attach the same way. `⌘⏎` sends now, interrupting the turn.

Paste a screenshot (`⌘V`) and it goes in as a markdown image: the thread shows it and the agent is handed it. Paste
files copied in Finder or the file manager, or drop them on the box, and each goes in as a link; a picture goes in as
an image. A file from outside the session is copied into the session's agent folder first, so a sandboxed agent can
read it too. Threads between the editor's lines take them the same way.

## Urgent

Sends the message in front of the running turn: the agent is interrupted and gets it at once.

## Btw

A side question, answered by a fork of the agent that has no tools and doesn't join the conversation.

## Between the lines {#inline}

The pointer on a line of a file, in the editor or a diff, shows a **+** in its gutter: a click opens a thread on that
line, to ask the agent about it or ask for a change. `ga` does the same on the cursor's line (`gc` or `ga` in a diff).
**Ask about this…** in the editor's right-click menu (or `thread.new`) opens a thread on the selection, or on the place
clicked, and the agent is told its line and column (`round.go:4:7`, or `round.go:3:6-4:2` for a selection): `⏎` sends
it, `⇧⏎` starts a new line, `Esc` drops it. Open threads show under their line with the agent's state, and take replies there
the same way.

## Summary

The session at a glance, at the window's right (`requests.summary`): its requests, the tasks rail (✓ done, ◐ in
progress, ○ to do) and the stats: context, cache, cost, tasks and the latest tests.

{{keys "request*"}}
