---
id: agent/requests
covers: [requests, request, requests.summary.toggle, thread.proposal.accept]
related: [agent/console, agent/asks, editor/pane]
---

# Requests

A request is a thread with the agent: a comment on a line, or about the whole change, and the replies to it. The
Requests view lists them (`⌘⇧R`, or the rail); one opens in the main area to read and answer. A thread between the
editor's lines and its request here are the same thread: reply in either.

## List

Every request of the session in front, grouped by what it waits on: **Working** (the agent has it), **Needs you** (the
agent answered: it never resolves a request, you do), **Open** (queued behind the turn, or a btw), **Resolved**. `j` and `k` move, `⏎` writes to the selected
one, `o` opens its anchor in the editor and `R` resolves it.

## Search

Narrows the list to requests whose title or comments hold the words.

## New request

Starts a request that isn't anchored to a line (`⌘N` in the list). It goes to the agent when you send it.

## Request

Its tab carries its name; the header shows only its state. Right-click the tab for **Rename…**, which edits the name
in the header. A session started from a request is named by a small model
(Claude's Haiku), and its first request shares the name; `sessions.auto_name` turns that off. The anchored code at the top, where it is now (it follows edits), then the conversation: your prompt as markdown with
what you pasted, and the agent's replies. **Open in editor**, in the header or on the code, goes back to the anchor
with the thread in view (**Open in plan** for a plan's doc, which opens in the plan), and a
click on one of the code's lines goes to that line; **Resolve** closes the request and
completes the tasks it asked for. At the right: where it is and who has it, the files it changed, the tasks and
requests it spawned, and other requests about the same file.

## Comments {#comments}

A comment reads as markdown the way GitHub shows a comment: each line you write stays a line (a blank line starts a
new paragraph), and a code block's long lines wrap, in a list item too. A markdown file's preview joins a
paragraph's lines, as a document does.

A comment's **⋯**, at its top right while the pointer is over it: **Copy** takes its text as it reads, **Copy as
markdown** its source, and **View markdown** shows the source in a dialog. While a message of yours waits for the agent
(queued), the menu also has **Edit**, which puts it back in the composer (sending replaces it, the messages after it
staying where they are), and **Delete**, which takes out that message alone. `↑` in an empty composer edits your last
queued message.

## Do it {#do-it}

A reply in which the agent proposes a change it hasn't made yet (it says so when it answers) ends with **Do it**, here
and between the lines. A click sends your "Do it" in the thread, just as if you'd typed it. The button goes once
anyone else writes in the thread. A reply that disagrees, answers a question, or reports a change already made gets no
button. A pull request review thread's draft reply that offers a fix gets it too, beside **↗ Forward**: the reviewer
sees nothing until you forward a reply. `thread.proposal.accept {thread}` does the same from keys or Lua.

## Evidence

A reply that says the tests pass with no passing run behind it is flagged ⚠. The runs themselves are in the agent
console, where a click opens one: the command, who ran it, what failed and what it printed.

## Composer

A request's tab, opened or switched back to, or a request picked in the list, puts you here, ready to reply; `Esc`
goes to the list. Write to the request: `⏎` sends, `⇧⏎` starts a new line, and the agent gets it at once when it's idle, or when its turn ends. `⌃a`
in a picker attaches what you pick; a terminal's output and a failing test attach the same way. `⌘⏎` sends now, interrupting the turn.

Paste a screenshot (`⌘V`) and it goes in as a markdown image: the thread shows it and the agent is handed it. Paste
files copied in Finder or the file manager, or drop them on the box, and each goes in as a link; a picture goes in as
an image. A file from outside the session is copied into the session's agent folder first, so a sandboxed agent can
read it too. Threads between the editor's lines take them the same way.

## Attach {#attach}

**+** under the box attaches a file on this machine: type its path (`~/` lists your home; a folder opens as you
pick it), or pick **Browse…** for the system's file chooser. It goes in as a link to a copy the agent can read, as a
dropped file does. In the conversation, a message's attached files show as cards under it: click one to open the file
in a tab, or **↓** to save a copy: type where (it starts in `~/Downloads`), pick a folder to save in, or **Browse…**.

## Urgent

Sends the message in front of the running turn: the agent is interrupted and gets it at once.

## Btw

A side question, answered by a fork of the agent that has no tools and doesn't join the conversation.

## Between the lines {#inline}

The pointer on a line of a file in the editor shows a **+** in its gutter: a click opens a thread on that line, to ask
the agent about it or ask for a change, whatever threads the line has already. In a diff, or a plan's docs, a click
on the line itself does the same. `ga` does the same on the cursor's line (`gc` or `ga` in a diff).
**Ask about this…** in the editor's right-click menu (or `thread.new`) opens a thread on the selection, or on the place
clicked, and the agent is told its line and column (`round.go:4:7`, or `round.go:3:6-4:2` for a selection), and its header names the line (`L4`, or `L3–4` for a selection): `⏎` sends
it (or **Ask**, **Comment** for the author, under the field), `⇧⏎` starts a new line, `Esc` (or **Cancel**) drops it. Open threads show under their line with the agent's state, and take replies there
the same way. **Full view** (the Requests icon) in a thread's header, between the lines or in a plan's margin, opens it here as a request.

## Summary

The session at a glance, at the window's right (`requests.summary.toggle`): its requests, the tasks rail (✓ done, ◐ in
progress, ○ to do) and the stats: context, cache, cost, tasks and the latest tests.

{{keys "request*"}}
