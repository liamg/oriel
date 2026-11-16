---
id: plugins/agentbus
covers: [agentbus, plugin.agentbus.*]
related: [shell/inbox, shell/hud, plugins/secrets]
---

# agentbus

agentbus is a small message bus on your team's tailnet: people's agents ask each other's humans questions and ask
for reviews. The agentbus plugin puts what comes for you in your inbox, and its process (`oriel-agentbus`) holds the
connection.

## Questions {#questions}

A question from someone's agent is a card with its choices as numbered buttons, and a field to type an answer. One
with a deadline counts down to it; leave it, and at the deadline your agent answers for you, from what it knows of
the session in front, with no tools: the answer says your human was away. **Later** puts a question off.

## Review requests {#reviews}

A review request shows the pull request, its repository and size. **Claim** takes it, so nobody else does; **Claim +
Open** takes it and starts a review session on it; **Ignore** puts it away. When you submit your review on GitHub,
it goes back to whoever asked, approved, changes requested or commented. **Ask agentbus** on a raised pull request
asks for a review of it; the assistant can too, once you say yes.

Reading a pull request and seeing your reviews need a GitHub token: set `GITHUB_TOKEN` (`:Secret GITHUB_TOKEN`; `gh
auth token` prints yours). It's sent only to api.github.com. Oriel's own GitHub calls find a token on their own
([Your GitHub token](git/github#token)).

## Presence {#presence}

**ACTIVE**, **IDLE** and **DND** at the foot of the Inbox panel, and in the HUD set what others
see. Do not disturb holds questions and review requests back until it ends.

## Auto-approve {#auto-approve}

`plugin.agentbus.auto_approve` holds rules, as JSON, that approve a review request on GitHub without showing it:
`[{ "name": "deps", "enabled": true, "when": { "all": [{ "repo": ["acme/.*"] }, { "paths": ["go.mod", "go.sum"] }] } }]`.
A condition is one of `all`, `any`, `repo` (patterns on owner/name), `author`, `requester`, `paths` (every changed
file matches one), `maxLines` and `checksGreen`; every way a rule can pass needs a `paths` or `maxLines` check. Your
own pull requests are never approved.

## The log {#log}

What came in and what was done, and what came of the reviews you asked for, newest first, in the HUD's agentbus
panel. It's kept in the plugin's data directory.

## Settings {#settings}

`plugin.agentbus.url` (where the bus is; empty finds `agentbus` on your tailnet), `plugin.agentbus.instance`,
`plugin.agentbus.asks`, `plugin.agentbus.reviews`, `plugin.agentbus.repos` (the repositories you take review requests
for: owner/name or a glob), `plugin.agentbus.presence`, `plugin.agentbus.auto_approve`.
