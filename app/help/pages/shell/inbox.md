---
id: shell/inbox
covers: [inbox, app.overlay.inbox, inbox.*]
related: [agent/asks, shell/tabs, shell/hud, plugins/lua]
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

## The title bar {#title-bar}

**INBOX** in the title bar counts the cards waiting on you; a click opens the panel. Each session's tab counts its
own, in the colour of the first.

## Adding cards {#plugins}

Cards come from inbox sources (`registry.InboxSource`): the asks of every session are one; a plugin adds its own with
`oriel.inbox.add`, and takes them away with `oriel.inbox.remove`.
