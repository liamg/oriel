---
id: shell/inbox
covers: [inbox, app.overlay.inbox, inbox.*]
related: [agent/asks, shell/tabs, shell/hud, plugins/lua, plugins/agentbus]
---

# The inbox

Everything waiting on you, from every session and every source: an agent's question or permission, a connection or
gateway call the sandbox holds, a security-key touch, a review request or a question from someone else's agent.

## Cards {#cards}

Cards waiting on you stack at the bottom right of the window, whatever is in front; past four, a count says how many
more wait. A security-key touch shows on its own at the top. Each card answers with its buttons, or with their keys
while it has focus. A card with a time limit shows a ring counting down to it. Its ✕, or `Esc` while it has focus,
dismisses it: it leaves the stack and waits on in the Inbox panel, still counted on its session's tab.

## The Inbox panel {#list}

The Inbox panel lists every card, those waiting first, then those answered, newest first. Click a card waiting on
you, or select it with `j` and `k`, and it opens under its row to be answered there. `Esc` or `q` closes the panel.

{{keys "inbox.*"}}

## Filters {#filters}

**All**, **Me** (for you by name), **Agents** (your own agents' asks), **Bus** (other people's agents),
**GitHub** and **Sandbox** (held connections and gateway calls).

## The panel's foot {#foot}

Plugins put what goes with the inbox at the foot of the Inbox panel: agentbus's presence, **ACTIVE**, **IDLE** and
**DND**, and the bus you're on.

## The title bar {#title-bar}

**INBOX** in the title bar counts the cards waiting on you; a click opens the panel. Each session's tab counts its
own, in the colour of the first.

## Notices {#notices}

A notice that asks you something (a question) is a card rather than a toast: a review request, or
any plugin's. It stacks while it waits and goes to the history once you answer or dismiss it. Notices that only tell
you something stay toasts, and are kept in the log.

## Dialogs {#dialogs}

A question that confirms something you just did (**Merge into main…**, discarding changes) is a dialog in the middle
of the window, not a card in the stack. Its buttons answer it; Escape or a click outside cancels it. A notice asks
this way when it sets `Confirm`; an inbox card when it sets `Dialog`.

## Prompts {#prompts}

An agent's permission is a prompt: it comes up in the middle of the window over a dimmed background, holding the
keys, so its keys (`y`, `a`, `n`…) answer it. It waits until no key has gone down for three seconds
(`inbox.prompt_pause`), so it never takes a key meant for what you're typing; if you've already stopped, it comes up
at once. Prompts come one at a time, in the order they came: once one is answered, here or anywhere else, or the agent
withdraws it, the next comes up straight away. Only the session in front prompts. A prompt in a session behind
waits, and that session's tab calls for you: it pulses in the prompt's colour a few times, then stays lit until you
go to it ([Session tabs](shell/tabs)), and going to it brings the prompt up at once. Escape, its ✕ or a click
outside puts a prompt off: it waits on in the Inbox panel and the agent console, and comes up again the next time
its session comes to the front. A card is a prompt when it sets `Prompt`.

{{options "inbox"}}

## Adding cards {#plugins}

Cards come from inbox sources (`uireg.InboxSource`): the asks of every session are one; a plugin adds its own with
`oriel.inbox.add`, and takes them away with `oriel.inbox.remove`. A card's `body` holds what its buttons can't: a
field to type an answer in.
