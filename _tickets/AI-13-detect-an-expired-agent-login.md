---
id: AI-13
title: "Detect an expired agent login, flag it on its worker, and fix it from Oriel"
phase: P5
status: review
area: Sessions and AI
module: agent, worker, app
depends_on: [AI-2, AI-3, AI-12]
---

# AI-13 · Detect an expired agent login, flag it on its worker, and fix it from Oriel

**Phase** 5 · AI (follow-up) · **After** [AI-2](AI-2-claude-provider-interactive-pty-plus-mod-hooks-n.md), [AI-3](AI-3-codex-provider-app-server-billing-checked-in-p0.md), [AI-12](AI-12-stats-and-usage-pages-grouped-by-repo.md) · **Unblocks** nothing yet

Added after P5 from review: in crit an agent whose login expired looks busy while the CLI waits on `/login`. Today a
login is checked only when a session starts (`Detect` reads Claude's credentials file).

## Design

- `_design/agents.md` (providers, the PTY, the transcript, AI-12's accounts)
- `_design/sessions.md` (status, needs you), `_design/errors.md` (notices)

## What it is

- **Detect it in the provider**, by two means, as no hook is sure to fire: the transcript (the API's auth error that
  Claude writes into its log, "Please run /login", an `authentication_failed` error type), and the agent's PTY screen
  (the login prompt, for expiry while idle). Either raises an `agent.AuthExpired{Provider, Account, Detail}` event.
  Codex raises it from its not-signed-in errors. Write detection against recorded real output, not guessed text.
- **On the worker:** the login is the worker's and the account's (AI-12), so every session on that worker using that
  account is flagged: status **needs you**, a card "Claude on devbox needs you to log in". The worker's provider state
  says signed out, which the launcher's provider card shows.
- **In the app:** a global notice naming the worker and the fix, and that account's usage indicator in the title bar
  marked.
- **The fix:** on a local worker the card's **Log in** opens the session's own Claude terminal (the raw PTY) with
  `/login` typed; on a remote worker the card says what to run there (`claude`, then `/login`). After a login the
  worker re-checks, clears every flag, and the waiting sessions carry on.

## Done when

- A fake Claude session whose transcript gets the recorded auth error, or whose screen shows the login prompt, raises
  AuthExpired; every session on that worker and account turns needs-you with the card; the notice and the indicator
  show; the launcher shows signed out.
- Log in on a local worker opens the raw terminal with `/login`; a fake successful login clears every flag and the
  sessions go on.
- A Codex not-signed-in error does the same for Codex.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
