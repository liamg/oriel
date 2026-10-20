---
id: agent/permissions
covers: [agent.permissions, permissions.tool, permissions.net]
related: [agent/asks, agent/console, agent/workspace]
---

# Permissions

What the sandboxed agent of the session in front may do, and what it did: the Permissions tab of the
[Workspace](agent/workspace) page. **Permissions and connections ›** under the console's Rules in force, or
`agent.permissions`, opens it there; `agent.permissions.dock` docks it at the bottom beside the console instead. A change made
here lasts for the session; for every session, use the `sandbox.*` settings. A deny in settings can't be lifted from
here.

An agent running on the host, outside a sandbox, has your access: none of this applies to it.

## Rules

**Waiting on you** lists the network and gateway asks open now, answered as on their cards.

## Tools {#tools}

Everything privileged is a tool on the sandbox's gateway: Oriel's own host actions, then each of your MCP servers.
Each row says what a call meets (allowed, asks you, denied) and where that comes from: this session, project
settings, your settings, always on this machine, read-only (allowed by default), or the built-in default. The menu
sets this session's own rule for the tool, or drops it.

## Network {#network}

The network rules in force, most local first: this session's, project settings, your settings, what you allowed
always, the endpoints the sandbox's environment points at (the model's `ANTHROPIC_BASE_URL`, which may reach private
and tailnet addresses), and the built-in list. The most specific rule for a destination wins. Anything else asks
you. Type a host (`api.example.com`, `*.example.com`, `host:443`) to allow or block it for the session; a session's
own rule has **Remove**. From Lua or a keymap, `permissions.net` takes `private = true` to let an allow reach LAN and
tailnet addresses.

When network filtering is off (`sandbox.egress_proxy`, or the session's network set to open), every connection is allowed and none of these rules apply. A docker sandbox's connections are still made from your machine, so what only it reaches (a VPN, a tailnet, its private DNS) stays reachable.

## Connections {#connections}

Every connection the agent made or tried, newest first: grouped by destination, or one row each. Encrypted
connections and ssh pass through unopened, so they show a host, a port and sizes; only plain HTTP shows a path and a
status. Each row says why it went the way it did, and **Allow** or **Block** changes the answer for the rest of the
session. A connection that never got through is also noted in the agent console's transcript, once per destination.
