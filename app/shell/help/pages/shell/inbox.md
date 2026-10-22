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

## Adding cards {#plugins}

Cards come from inbox sources (`uireg.InboxSource`): the asks of every session are one; a plugin adds its own with
`oriel.inbox.add`, and takes them away with `oriel.inbox.remove`. A card's `body` holds what its buttons can't: a
field to type an answer in.
